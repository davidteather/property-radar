import {
	defineRailway,
	github,
	postgres,
	preserve,
	project,
	service,
	volume,
} from "railway/iac";

// This file is authoritative for the linked project: omitting a resource or an
// env key here deletes it on `railway config apply`.
//
// Shape: Postgres + VersityGW (a tiny S3-over-POSIX gateway for thumbnails) +
// mcpd (MCP + REST + /docs on one port, owns migrations) + an always-on crawl worker.
// Shared S3 storage lets mcpd and the crawler share one thumbnail cache.
export default defineRailway(() => {
	const db = postgres("Postgres");

	// Every service builds from this repo with the root as context; forks point it at their own.
	const REPO = "davidteather/property-radar";

	// Only commits touching the Go build (sources, embedded assets, module files,
	// Dockerfiles) redeploy the Go services; a docs-only push must not interrupt a
	// running crawl. gitignore-style, relative to the repo root.
	const GO_WATCH = ["cmd/**", "internal/**", "migrations/**", "go.mod", "go.sum", "deploy/Dockerfile*"];

	// Seconds between SIGTERM and SIGKILL on redeploy (Railway's default is 0).
	// mcpd/console drain HTTP for 15s; the crawler needs ~15s after cancel to mark
	// an interrupted target pending so it re-queues instead of stalling.
	const DRAIN_SECONDS = 30;

	// The S3 gateway's data volume (re-derivable thumbnail cache). No region set:
	// the deploying account's default applies. If you pin one later, keep it pinned —
	// `railway config apply` treats a removed region as a destructive volume change.
	const storageData = volume("storage-data", { sizeMB: 5000 });

	// VersityGW: a single-binary Go S3 gateway over a POSIX directory (MinIO idles at
	// ~150 MB for the same tiny bucket); GOMEMLIMIT/GOGC keep idle RSS near the binary floor.
	const storage = service("versitygw", {
		// Thin wrapper image (deploy/versitygw/Dockerfile) whose entrypoint creates the
		// object dir a fresh volume lacks, then starts the gateway. Built from
		// the repo because Railway's start-command quoting could not carry the mkdir.
		source: github(REPO, { branch: "main" }),
		build: { watchPatterns: ["deploy/versitygw/**"] },
		volumeMounts: { "/data": storageData },
		// Railway meters the cgroup total, which counts the kernel's page cache of the
		// thumbnail files; the cap makes the kernel evict it (the process itself is ~40 MB).
		deploy: { limitOverride: { containers: { memoryBytes: 256 * 1024 * 1024 } } },
		env: {
			RAILWAY_DOCKERFILE_PATH: "deploy/versitygw/Dockerfile",
			ROOT_ACCESS_KEY: "radar",
			// The S3 secret for the thumbnail bucket. `${{secret(48)}}` makes Railway
			// generate one on first apply and reuse it thereafter, so a fresh
			// deploy/template self-provisions (mcpd + crawler read it via
			// ${{versitygw.ROOT_SECRET_KEY}}, so they stay in sync). Applying this over
			// an already-preserved secret rotates it once — harmless (it is API auth,
			// not encryption; the POSIX-backed objects are unaffected).
			ROOT_SECRET_KEY: "${{secret(48)}}",
			GOMEMLIMIT: "64MiB",
			GOGC: "50",
			// Return freed pages to the OS eagerly so billed RSS tracks the live heap.
			GODEBUG: "madvdontneed=1",
		},
	});

	// Every service reads the bucket over the private network (free, stays in-datacenter).
	const s3Env = {
		STORAGE_BACKEND: "s3",
		S3_ENDPOINT: "${{versitygw.RAILWAY_PRIVATE_DOMAIN}}:9000",
		S3_BUCKET: "thumbs",
		S3_ACCESS_KEY: "${{versitygw.ROOT_ACCESS_KEY}}",
		S3_SECRET_KEY: "${{versitygw.ROOT_SECRET_KEY}}",
		S3_REGION: "us-east-1",
		S3_USE_SSL: "false",
	};

	// One service, one URL, one token: mcpd serves the MCP stream at the root and
	// the REST API (/v1) + docs (/docs) on the same port.
	const mcpd = service("mcpd", {
		source: github(REPO, { branch: "main" }),
		build: { watchPatterns: GO_WATCH },
		// ALWAYS: a dependency that is down longer than the startup retry budget
		// must not exhaust Railway's default on-failure restart count.
		deploy: { restartPolicyType: "ALWAYS", drainingSeconds: DRAIN_SECONDS },
		healthcheck: "/healthz",
		// Must exceed mcpd's startup: up to 2 minutes each waiting for Postgres
		// and the S3 gateway on a co-deploy, then goose migrations, before
		// /healthz answers. Raise if a large/slow migration is ever added.
		healthcheckTimeout: 300,
		replicas: 1,
		env: {
			// deploy/Dockerfile with the repo root as build context; no start
			// command, because distroless has no shell to run one.
			RAILWAY_DOCKERFILE_PATH: "deploy/Dockerfile",
			DATABASE_URL: db.env.DATABASE_URL,
			// mcpd applies embedded goose migrations on startup (MIGRATE_ON_START
			// defaults true); it is the only service that migrates.
			...s3Env,
			// One token for both MCP and REST; generated once and kept out of git.
			MCP_BEARER_TOKEN: preserve(),
			// Optional one-shot seed of a first standing scope (docs/deploying/railway.md);
			// kept across applies so a re-apply before first boot does not wipe it.
			SEED_CRAWL_AREAS: preserve(),
			// Gates the public /img photo proxy: with it set, image URLs must carry
			// ?k=<this> (tools/console append it automatically), so the proxy isn't an
			// anonymous rehost of provider photos. Read-only and safe to embed in <img>;
			// empty leaves the proxy fully public. Recommended for public deploys.
			PUBLIC_IMG_TOKEN: preserve(),
			// Accept the token in a ?token= query too, so URL-only web connectors
			// (claude.ai / ChatGPT) can connect without a header.
			URL_TOKEN_AUTH: "true",
			// The web console get_console_url points users to (optional service below).
			// Until console has a public domain this renders as "https://", which mcpd
			// drops with a warning rather than handing out a broken link.
			CONSOLE_URL: "https://${{console.RAILWAY_PUBLIC_DOMAIN}}",
		},
	});

	// The cloud crawler: an always-on queue worker (`ingest -from-targets
	// -proxy-mode all -watch`, the image's CMD). It drains due crawl targets, then
	// sleeps on a Postgres crawl_due notification (poll fallback), so agent-queued
	// requests run within moments; standing scopes re-crawl about hourly and
	// deep roughly daily. Idle cost is a sleeping Go process (a few MB).
	// `all` routes the GraphQL API and site pages through the residential proxy
	// pool — the only config in which cloud crawling reaches StreetEasy's API.
	// With a datacenter/free Webshare plan the API is PX-blocked and the worker
	// idles honestly; the fallback is crawling from the operator's own machine.
	const crawler = service("crawler", {
		source: github(REPO, { branch: "main" }),
		build: { watchPatterns: GO_WATCH },
		env: {
			RAILWAY_DOCKERFILE_PATH: "deploy/Dockerfile.ingest",
			DATABASE_URL: db.env.DATABASE_URL,
			...s3Env,
			// Residential Webshare key, set by the operator in the dashboard.
			// A datacenter/free key makes the API calls PX-block.
			WEBSHARE_API_KEY: preserve(),
			// Rolling-cache window for cached thumbnails, in days. The crawler evicts
			// the bytes of any photo not re-verified within this window (and instantly
			// on delist), so the store is a cache, not an archive. "0" disables it.
			PHOTO_TTL_DAYS: "30",
		},
		deploy: {
			// A crashed worker restarts; there is no cron schedule any more.
			restartPolicyType: "ALWAYS",
			drainingSeconds: DRAIN_SECONDS,
		},
	});

	// OPTIONAL web console (cmd/console): a tiny htmx UI over the REST API, login-gated
	// by the bearer token (kept in an http-only cookie, never in this service's env).
	// To turn it off, delete this service, drop it from `resources`, and remove
	// mcpd's CONSOLE_URL above (its only dependent).
	const consoleSvc = service("console", {
		source: github(REPO, { branch: "main" }),
		build: { watchPatterns: GO_WATCH },
		deploy: { restartPolicyType: "ALWAYS", drainingSeconds: DRAIN_SECONDS },
		healthcheck: "/healthz",
		env: {
			RAILWAY_DOCKERFILE_PATH: "deploy/Dockerfile.console",
			// Reads the REST API over the public URL so photo links resolve for the browser;
			// only small JSON crosses the wire (images load browser->mcpd).
			API_BASE_URL: "https://${{mcpd.RAILWAY_PUBLIC_DOMAIN}}",
			// Writes are ON by default; add CONSOLE_READONLY: "true" to make it view-only.
		},
	});

	return project("property-radar", {
		resources: [db, storageData, storage, mcpd, crawler, consoleSvc],
	});
});
