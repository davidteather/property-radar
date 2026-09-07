# Railway deployment

Everything needed to put `mcpd` on Railway: first for yourself at the lowest
footprint the $5 Hobby plan allows, then as a published template that other
people can deploy in three clicks.

All Railway facts below were verified against the live docs on **2026-08-24**;
every claim links to the page it came from at the end of this file.

## What runs where

```text
                              RAILWAY project (one private network)          CLIENT
                     ┌──────────────────────────────────────────────┐     Claude Code
  crawler (worker) ──┤  Postgres ◀── mcpd (migrates, MCP + REST) ────┼──▶      │
  ingest -from-targets    ▲             │  reads thumbnails            │        │
  -proxy-mode all -watch  │             ▼                             │  https://*.up.railway.app
     │ (residential        └──────  VersityGW (S3, thumbs bucket) ◀────┘   (bearer token)
     │  proxies)                                    ▲
     └── writes thumbnails ─────────────┘

  LAPTOP fallback (residential IP): bin/ingest -from-targets -proxy-mode split
      writes to the same Postgres + VersityGW over `railway connect --tunnel-only`.
```

**One server, one URL, one token.** `mcpd` serves the MCP stream at the root
(`https://<host>/`) **and** the REST API at `/v1` **and** interactive Scalar docs
at `/docs`, all on the same port, all gated by the same `MCP_BEARER_TOKEN`
(`/healthz`, `/connect`, `/docs`, `/openapi.json`, `/schemas/*` stay open, and
`/img/*` is open until `PUBLIC_IMG_TOKEN` gates it). There is no separate API
service, so a deployer gets a single endpoint.
`claude mcp add … https://<host>` still points at the root, so
already-registered clients keep working.

Two things changed from the volume-based design:

- **Thumbnails live in VersityGW (S3-compatible object storage), not a volume.** A
  Railway volume attaches to a single service, so the crawler could not write
  thumbnails that `mcpd` reads. Object storage (the
  [photos & storage decision](../../docs/decisions.md)) removes that wall:
  both services set `STORAGE_BACKEND=s3` against the VersityGW service on the
  private network, and each unique thumbnail is written once (write-once by
  content-addressed key). (Railway also has a native `bucket()` resource; the
  template uses VersityGW so the same recipe runs on any S3-compatible host.)
- **The crawler CAN run on Railway now — with an honest caveat.** The template
  ships a `crawler` worker service running `ingest -from-targets -proxy-mode all -watch`,
  which routes **both** the StreetEasy API and site pages through the Webshare
  pool. That reaches the API **only with a residential Webshare plan**. With a
  datacenter or free plan the API calls are PX-blocked (the
  [provider-access decision](../../docs/decisions.md)) and the worker
  no-ops each pass until you configure residential proxies. The honest fallback is
  running `ingest -from-targets` from your own machine (below). `mcpd` never
  crawls, so it still needs no `WEBSHARE_API_KEY`; that key goes on the
  **crawler** service (or your laptop).

## Config as code: `.railway/railway.ts`, not `railway.json`

Railway **deprecated `railway.json` / `railway.toml`**: "Config as Code is
deprecated… Existing `railway.json` / `railway.toml` files continue to work for
services that already use them until **2026-12-01** (hard cutoff). **New
services cannot opt into Config as Code.**" A `railway.json` committed today
would simply never be read for a service created today.

The replacement is **Infrastructure as Code**, a `.railway/railway.ts` at the
repo root (`.py` and `.go` mirrors exist; TypeScript is "the most mature
surface"). This repo ships one: see [`../../.railway/railway.ts`](../../.railway/railway.ts).
It declares six resources: the Postgres service, a `storage-data` volume, a
VersityGW service (built from `deploy/versitygw/Dockerfile`, S3 on `:9000`, no
web UI), the `mcpd` service (built from `deploy/Dockerfile`, reads thumbnails
from VersityGW, applies migrations), the `crawler` worker service (built from
`deploy/Dockerfile.ingest`, always-on, `restartPolicyType: ALWAYS`), and the
optional `console` web UI (built from `deploy/Dockerfile.console`, talks to
mcpd's REST API over its public URL), plus the healthchecks and the S3/DATABASE
variable wiring. The IaC types
were confirmed against the bundled `railway` npm package
(`cd .railway && npx tsc --noEmit`), which exposes `deploy.cronSchedule`,
`deploy.restartPolicyType`, `image()`, `start`, and `volumeMounts`.

The crawler is an always-on worker, not a cron: it drains due targets, then
sleeps on a Postgres `crawl_due` notification (with a `-poll` fallback), so an
agent's `request_crawl` runs within moments; standing scopes re-run only once
their last run is `-standing-every` (default 1h) old, so idle polls cost nothing. `restartPolicyType: ALWAYS` brings
it back after a crash. Railway's cron mode (`deploy.cronSchedule`, 5-field
crontab, exit-to-complete, 5-minute minimum) remains an option for an operator
who prefers scheduled runs; pair it with `-from-targets` without `-watch` and
`restartPolicyType: NEVER`. Either way the service shares the project's private
network, so it reaches Postgres and VersityGW over `*.railway.internal`.

Two constraints that shaped the file:

- **`.railway/` must sit at the repo root.** Railway looks for it there; the
  path is not configurable the way the old per-service "Railway Config File"
  setting was. The Go toolchain ignores directories starting with `.`, so
  `.railway/railway.ts` is invisible to `go build ./...`, `go vet`, and
  `make check`.
- **The file is authoritative — "omit means delete."** Anything you add in the
  dashboard and forget to mirror here is removed on the next `railway config
  apply`. Treat the dashboard as read-only once you adopt it. The one exception
  is generated `*.up.railway.app` domains: they are outside IaC (only custom
  `domains:` are declared) and survive an apply.
- **Your editor needs the types.** `railway.ts` imports from `railway/iac`,
  which the Railway CLI resolves with its own bundled runtime; tsserver can't,
  so the file reads as `Cannot find module 'railway/iac'` in an editor.
  `.railway/package.json` + `.railway/tsconfig.json` pin the `railway` npm
  package for its type declarations only; run `npm install` in `.railway/` once
  and the error goes away. `.railway/node_modules/` is gitignored, nothing in
  the Go build or `deploy/Dockerfile` reads it, and `railway config plan` does
  not need it.

  ```bash
  cd .railway && npm install && npx tsc --noEmit
  ```

Apply it:

```bash
railway link                # link this directory to the project
railway config plan         # dry run: shows the diff, changes nothing
railway config apply        # confirm, then apply
```

IaC is still experimental and does **not** cover the Serverless toggle or a
Dockerfile path; those stay in service settings (or, for the Dockerfile, in a
variable, which is what this repo does):

| Setting | Where it lives | Value for this stack |
|---|---|---|
| Dockerfile path (mcpd) | `RAILWAY_DOCKERFILE_PATH` variable (set in IaC `env`) | `deploy/Dockerfile` |
| Dockerfile path (crawler) | `RAILWAY_DOCKERFILE_PATH` variable | `deploy/Dockerfile.ingest` |
| Dockerfile path (console) | `RAILWAY_DOCKERFILE_PATH` variable | `deploy/Dockerfile.console` |
| Healthcheck (mcpd, console) | IaC `healthcheck` / `healthcheckTimeout` | `/healthz`, 60s (mcpd) |
| Restart policy (mcpd, console) | IaC `deploy.restartPolicyType` | `ALWAYS` (pending `railway config apply`; the dashboard default is on-failure, 10 retries) |
| Restart (crawler) | IaC `deploy.restartPolicyType` | `ALWAYS` (always-on queue worker, no cron) |
| Watch paths (Go services) | IaC `build.watchPatterns` | `cmd/**`, `internal/**`, `migrations/**`, `go.mod`, `go.sum`, `deploy/Dockerfile*` — a docs-only push does not redeploy |
| Draining seconds (Go services) | IaC `deploy.drainingSeconds` | `30` (Railway's default is 0: SIGKILL right after SIGTERM, mid-crawl) |
| Start command (mcpd/crawler) | **leave empty** | the image's `ENTRYPOINT`/`CMD` |
| Start command (versitygw) | **leave empty** | the Dockerfile's `ENTRYPOINT` runs the gateway |
| Serverless (sleep) | Service settings → Deploy → Serverless | **off** (see cost section) |

Leave the start command empty on purpose: `deploy/Dockerfile` produces a
`gcr.io/distroless/static-debian12` image with **no shell**, and a Railway
start command is a command line, not an argv array. The image already runs
`mcpd -transport http` (no `-addr`, so `$PORT` applies).

## Ports: how `-addr` and `$PORT` interact

Railway "automatically injects" `PORT` and expects the server to bind
`0.0.0.0` on it; a server that ignores `PORT` gets a 502 "Application failed to
respond" from the edge proxy.

`mcpd` resolves its listen address like this:

1. `-addr` not passed **and** `PORT` set → listens on `:$PORT`. This is the
   Railway path; the Dockerfile's `CMD` deliberately omits `-addr` so it works.
2. `-addr` passed explicitly (even as `:8787`) → that address wins, `PORT` is
   ignored. This is the VPS/compose path (`MCP_PUBLISH_ADDR`, Caddy on
   loopback, etc.).
3. No `PORT`, no `-addr` → `:8787`, exactly as before.

Nothing needs to be configured on Railway for this. If you ever *do* pin
`-addr`, also set a `PORT` variable to the same number, because the healthcheck
probes `PORT`.

---

# Part A: deploy your own instance

Prerequisites: a Railway account on **Hobby** ($5/mo, a card is required; there
is no durable free path), the Railway CLI (`brew install railway`), this repo
pushed to GitHub, and `goose` reachable via `go run` from the repo.

### 1. Create the project and Postgres

No account yet? Sign up via
<https://go.dteather.com/railway?src=property-radar&placement=railway-guide>
(supports this project). Then: Dashboard → **New Project** → **Deploy PostgreSQL**. Name the service
`Postgres` (the IaC file and the reference variables below assume that name).
Railway databases are "unmanaged services", so you own backups and tuning.
Confirm the major version once it is up with `railway connect Postgres` then
`select version();`; the migrations in `migrations/` target Postgres 17.

### 2. Add the `mcpd` service from this repo

**New** → **GitHub Repo** → `davidteather/property-radar`. Then, in the new
service's **Variables**, add:

```
RAILWAY_DOCKERFILE_PATH=deploy/Dockerfile
```

Railway only auto-detects a `Dockerfile` at the repository root; this variable
points it at ours. The build context stays the repo root, which is what
`deploy/Dockerfile` requires (`COPY go.mod go.sum ./`, `COPY . .`).

Viewers of a published template never fork the repo; the template builds this
repository directly.

### 3. Add VersityGW for thumbnails (object storage, not a volume)

Thumbnails go in an S3-compatible bucket so the crawler and `mcpd` can share
them without a single-service volume (the
[photos & storage decision](../../docs/decisions.md)). VersityGW is a tiny
S3-over-POSIX gateway that keeps idle RAM near its binary floor. Add a VersityGW
service:

- **New** → **GitHub Repo** → `davidteather/property-radar`, then set
  `RAILWAY_DOCKERFILE_PATH=deploy/versitygw/Dockerfile`. This is a thin wrapper
  over `versity/versitygw:v1.7.0` whose entrypoint creates the object + sidecar
  dirs a fresh volume lacks, then runs the gateway, so there is no Docker image
  to pick and **no start command** to set (the Dockerfile's `ENTRYPOINT` runs it).
- **Volume**: attach one at mount path **`/data`** (VersityGW's own data dir,
  its storage rather than a shared thumbnail mount). ~2 GB covers the v0 corpus.
- **Variables**: `ROOT_ACCESS_KEY=radar` and `ROOT_SECRET_KEY` =
  `openssl rand -hex 32` (these double as the S3 credentials below), plus
  `GOMEMLIMIT=64MiB`, `GOGC=50`, and `GODEBUG=madvdontneed=1` to keep idle RSS
  near the binary floor.

VersityGW's S3 API is on `:9000` and it has **no web console**. Do **not** generate
a public domain for it; the other services reach it privately at
`versitygw.railway.internal:9000`. To browse the bucket, point the `mc` client or
aws-cli at the `:9000` S3 API over a `railway connect` tunnel or a temporary
public domain (that is how you can eyeball that thumbnails are landing).

Railway private networking is IPv6-internally; minio-go connects by the
`versitygw.railway.internal` hostname, which resolves on the private network, so no
special client config is needed.

### 4. Variables

On the `mcpd` service:

| Variable | Value | Why |
|---|---|---|
| `DATABASE_URL` | `${{Postgres.DATABASE_URL}}` | Reference variable, `${{ServiceName.VAR}}` syntax (namespace = the service's name). **Use `DATABASE_URL`, not `DATABASE_PUBLIC_URL`**: the private one goes over `*.railway.internal`, where "internal traffic doesn't count toward egress billing". |
| `STORAGE_BACKEND` | `s3` | Read thumbnails from VersityGW instead of local disk. |
| `S3_ENDPOINT` | `${{versitygw.RAILWAY_PRIVATE_DOMAIN}}:9000` | VersityGW's private address. |
| `S3_BUCKET` | `thumbs` | Created at startup by whichever service connects first. |
| `S3_ACCESS_KEY` | `${{versitygw.ROOT_ACCESS_KEY}}` | Reference the VersityGW root access key. |
| `S3_SECRET_KEY` | `${{versitygw.ROOT_SECRET_KEY}}` | Reference the VersityGW root secret key (never logged). |
| `S3_REGION` | `us-east-1` | Any value VersityGW accepts. |
| `S3_USE_SSL` | `false` | Internal HTTP over the private network. |
| `MCP_BEARER_TOKEN` | `openssl rand -hex 32` | `mcpd` refuses to start `-transport http` without it. Never logged. |
| `URL_TOKEN_AUTH` | `true` | Lets URL-only web connectors (claude.ai, ChatGPT) pass the token as `?token=`. Omit for header-only auth. |
| `PUBLIC_IMG_TOKEN` | _(optional)_ `openssl rand -hex 16` | Gates the `/img` photo proxy; tools append `?k=` automatically. Empty leaves it public. |
| `CONSOLE_URL` | _(optional)_ `https://${{console.RAILWAY_PUBLIC_DOMAIN}}` | Where `get_console_url` points; only if you also deploy the optional `console` service (see [What runs where](#what-runs-where)). |
| `RAILWAY_DOCKERFILE_PATH` | `deploy/Dockerfile` | From step 2. |

`mcpd` auto-applies migrations on startup (`MIGRATE_ON_START` defaults true), so
no `THUMBS_DIR` and no migration step are needed on Railway. Do **not** set
`WEBSHARE_API_KEY` on `mcpd`; it never crawls. The key goes on the crawler
(step 10) or your laptop.

Private networking is per project *and* environment. Environments created after
2025-10-16 resolve `*.railway.internal` on both IPv4 and IPv6 (legacy ones are
IPv6-only); either way pgx and minio-go need nothing special. Note that the
private network is runtime-only. It does not exist during the build, so `mcpd`
applies migrations at container start (not in a build step), and VersityGW must be
up before the first crawl writes thumbnails.

### 5. Healthcheck

Service → **Settings → Deploy → Healthcheck Path** = `/healthz` (already in the
IaC file). Railway queries the path until it gets an HTTP `200`, from hostname
`healthcheck.railway.app`, on the port in `PORT`; default timeout is 300s, we
set 60. `mcpd` serves `/healthz` unauthenticated precisely so probes need no
secret.

Note for later: "Railway does not monitor the healthcheck endpoint after the
deployment has gone live." It is a deploy gate, not an uptime monitor, and it
will **not** keep a sleeping service awake.

### 6. Generate the domain

Service → **Settings → Networking → Generate Domain** (or `railway domain`).
Railway detects the listening port automatically when there is only one. You
get `https://<something>.up.railway.app` with TLS terminated for you; the
Caddy/Tailscale step from the VPS runbook disappears.

Smoke-test it:

```bash
curl -fsS https://<your>.up.railway.app/healthz              # -> ok (open)
curl -si  https://<your>.up.railway.app/ | head -1           # -> HTTP/2 401 (MCP root, needs token)
curl -si  https://<your>.up.railway.app/v1/state | head -1   # -> HTTP/2 401 (REST, needs token)
curl -fsS https://<your>.up.railway.app/openapi.json | head -c 40   # spec (open)
# then open https://<your>.up.railway.app/docs in a browser for the Scalar UI
```

### 7. Migrations (automatic)

**`mcpd` applies the embedded goose migrations itself on startup** and is the
only service that does. The crawler never migrates: it starts concurrently, and
`mcpd` owns the schema (`mcpd` refuses to serve on a partial schema, so a bad
migration fails the deploy loudly). Nothing to run by hand. `MIGRATE_ON_START`
defaults true; set it `false` (or `-migrate=false`) only if you migrate out of
band.

To migrate manually anyway — a rollback, or applying before first boot — open a
tunnel and run goose:

```bash
railway link
railway connect Postgres --tunnel-only -P 15432   # prints host/port/user/password/URL, holds the tunnel until Ctrl+C
# in a second shell, paste the printed URL:
go run github.com/pressly/goose/v3/cmd/goose@v3.27.3 \
  -dir ./migrations postgres "<the URL railway printed>" up
```

`railway connect` auto-tunnels over SSH when Postgres has no public TCP proxy,
so it never becomes internet-reachable; `-P` pins the local port. Two
alternatives, both worse here:

- **TCP Proxy + `DATABASE_PUBLIC_URL`** (Settings → Networking → Public Access,
  or `railway tcp-proxy create --port 5432`). Easy to script, but it exposes
  Postgres on `*.proxy.rlwy.net` and Railway states plainly that "you will be
  billed for Network Egress when using the TCP Proxy."
- **`railway run <cmd>`** injects the service's variables locally, but the
  `DATABASE_URL` it injects is the private `*.railway.internal` one your laptop
  cannot resolve, and sealed variables are not provided to `railway run` at
  all.

### 8. Register the server with Claude Code

`$MCP_BEARER_TOKEN` is the value you set in step 4 (the `mcpd` service's
Variables tab, or `railway variables --service mcpd`):

```bash
claude mcp add property-radar --transport http https://<your>.up.railway.app \
  --header "Authorization: Bearer $MCP_BEARER_TOKEN"
```

Add `--scope user` to get it in every project. Verify with `claude mcp list`,
then ask the model to call `get_state`. URL-only web connectors (claude.ai,
ChatGPT) cannot send a header; the IaC sets `URL_TOKEN_AUTH=true` so they can
connect with `https://<your>.up.railway.app/?token=<MCP_BEARER_TOKEN>` instead
(see the [transport decision](../../docs/decisions.md)).

### 9. Crawl targets (start empty; agent- or operator-seeded)

Both crawl paths read the `crawl_targets` table: `-from-targets` crawls every
enabled `standing` row plus every pending `once` row (the caller model queues
those with `request_crawl`). A fresh install starts **empty** by design — no
hardcoded scope, and nothing crawls a provider until asked. The corpus fills one
of two ways: the caller LLM notices the empty corpus and queues what the user
wants, or you set **`SEED_CRAWL_AREAS`** on `mcpd` (comma-separated area ids) to
pre-seed one standing sale scope on first boot. For reference, the NYC area ids
this project was built against include:

- **Brownstone Brooklyn**: areas `304,305,306,319,320,321,322,324,326,364`
- **Upper West Side sale**: area `135`

Once a scope exists (agent-queued or `SEED_CRAWL_AREAS`), the worker (with
residential proxies) or a laptop crawl populates it. Confirm with
`list_crawl_targets`, `GET /v1/crawl-targets`, or a query. To add scopes
directly (rentals, other neighborhoods), insert rows over a tunnel:

```bash
railway connect Postgres --tunnel-only -P 15432 &
psql "postgres://postgres:<pw>@127.0.0.1:15432/railway?sslmode=disable" -c "
INSERT INTO crawl_targets (kind, areas, listing_type, enabled, note)
VALUES ('standing', ARRAY['135','304','305','306'], 'rent', true, 'rentals');"
```

Keep an area set identical to any `-areas` list you were passing before so the
scope hash, and with it the volume-drift baseline, stays the same.
`bin/ingest -areas … -listing-type sale` is still there for one-off crawls
(exactly one of `-areas` and `-from-targets` is required).

### 10. Running the crawl

**The cloud worker (the self-contained path, residential proxies required).**
The template's `crawler` service runs `ingest -from-targets -proxy-mode all
-watch` as an always-on queue worker: it drains due targets, then sleeps on a
Postgres `crawl_due` notification with a poll fallback, so agent-queued requests
run within moments. `-proxy-mode all` routes **both** the
StreetEasy API and site pages through the Webshare pool. Set on the crawler
service:

| Variable | Value |
|---|---|
| `WEBSHARE_API_KEY` | your **residential** Webshare key ([how to get one](../../docs/deploying/railway.md#getting-a-webshare-key)) |
| `DATABASE_URL` | `${{Postgres.DATABASE_URL}}` |
| `STORAGE_BACKEND` / `S3_*` | same values as `mcpd` (step 4) |
| `RAILWAY_DOCKERFILE_PATH` | `deploy/Dockerfile.ingest` |

The worker picks up due targets on its own; to force a full pass, redeploy the
crawler (`railway redeploy` or dashboard → the crawler → Deploy). Each pass logs
a `crawl targets drained` summary with any failed, incomplete, or suspect target.

**Honest caveat** (the [provider-access decision](../../docs/decisions.md)).
`-proxy-mode all` reaches the API **only with a residential Webshare plan**.
With a **datacenter or free** plan the API calls are PX-blocked and the run
no-ops (a provider-level failure that cannot mass-delist) until you configure
residential proxies. That is not a broken deploy: Postgres, VersityGW, and
`mcpd` all work; only the cloud crawl is gated on residential IPs.

**The fallback that always works: crawl from your own machine** against the same
Railway Postgres + VersityGW. `-proxy-mode split` (API direct from your residential
IP, site pages via the pool) is the right mode here:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd /path/to/property-radar
railway connect Postgres --tunnel-only -P 15432 &   # keep the tunnel up for the crawl
trap 'kill %1' EXIT
sleep 5

export DATABASE_URL="postgres://postgres:<pw>@127.0.0.1:15432/railway?sslmode=disable"
# Write thumbnails straight into the Railway VersityGW bucket. Reach VersityGW's S3 API
# from the laptop with `railway connect versitygw` / a port-forward to :9000, or a
# public domain on the VersityGW service; then point S3_ENDPOINT at that address.
export STORAGE_BACKEND=s3
export S3_ENDPOINT=127.0.0.1:9000    # your forwarded VersityGW address
export S3_BUCKET=thumbs S3_ACCESS_KEY=radar S3_SECRET_KEY=<versitygw secret key>
export S3_REGION=us-east-1 S3_USE_SSL=false
# get a key: https://go.dteather.com/webshare?src=property-radar&placement=railway-guide
export WEBSHARE_API_KEY=...

bin/ingest -from-targets -proxy-mode split
```

(Or keep `STORAGE_BACKEND=local` on the laptop and skip VersityGW entirely; then
photos only appear once you point `mcpd` at wherever those bytes live. Sharing
the VersityGW bucket is what makes cloud `mcpd` serve the photos your laptop
fetched.)

### 11. Backfilling an existing local thumbnail cache into VersityGW

If you already have a `~/.local/share/property-radar/thumbs` tree from the VPS
or local-disk setup, copy it into the bucket **as-is**: the on-disk paths are
already the object keys (`streeteasy/kk/key.jpg`), so no transformation is
needed. Using the `mc` client against a forwarded VersityGW `:9000`:

```bash
mc alias set railway http://127.0.0.1:9000 radar <versitygw secret key>
mc mb --ignore-existing railway/thumbs
mc mirror --overwrite ~/.local/share/property-radar/thumbs/ railway/thumbs/
```

`mc mirror` is incremental, so re-running it only uploads genuinely new files
(thumbnails are write-once). Backfilled objects lack the width/height user
metadata that fresh crawls set, but that is provenance only. The MCP read path
decodes the bytes, so photos display correctly either way, and the next crawl
still skips them (a `StatObject` HEAD sees they exist and PUTs nothing).

**Doing nothing is a valid state.** `mcpd` treats an unreadable/absent thumbnail
as *missing* — skipped and counted, never a tool error — so an empty bucket
still answers every tool with text and returns whatever photos it has. Deploy
first, let the worker (or a laptop crawl) fill the bucket, seed a backfill only if
you have one. Sizing: the v0 corpus is ~1–3 GB of thumbnails.

---

# Part B: publish it as a template

Templates are composed in the dashboard from a project that already works, so
finish part A first.

### 1. Generate the template

Project page → **Settings** (top-right of the canvas) → scroll to **Generate
Template from Project** → **Create Template**. That drops you in the template
composer with your services pre-filled. (To start from nothing instead:
workspace settings → Templates → **New Template**, at
<https://railway.com/workspace/templates>.) In the composer you can set
per-service root directory, public networking, start command and healthcheck
path, and right-click a service to **Attach Volume** with a mount path.

Deployers do not fork anything: "As of March 2024, the default behavior for
deploying templates is to attach to and deploy directly from the template
repository. Therefore, you will **not** automatically get a copy of the
repository on deploy." A deployer who wants their own copy uses the service's
**Eject** action. The repository does have to be public (or the deployer must
have access), which `davidteather/property-radar` satisfies.

The generator copies the live project but **blanks every literal value** (it
cannot tell `S3_BUCKET=thumbs` from a secret) and keeps only `${{…}}`
references, and it does not read `.railway/railway.ts`. So the composer pass
below is where each literal gets typed back in, once. That pass is what turns
"N variable values needed" on the deploy page into zero.

Check, service by service:

- **Postgres**: Railway's Postgres service, untouched.
- **versitygw**: source is this public GitHub repo (built from
  `deploy/versitygw/Dockerfile`), empty start command (the Dockerfile's
  `ENTRYPOINT` runs the gateway), volume at `/data`, `ROOT_ACCESS_KEY`/
  `ROOT_SECRET_KEY` set, **no console**, **no public domain**.
- **mcpd**: source is this public GitHub repo (viewers never fork it),
  `RAILWAY_DOCKERFILE_PATH=deploy/Dockerfile`, healthcheck `/healthz`, empty
  start command, no volume (thumbnails are in VersityGW).
- **crawler**: same repo, `RAILWAY_DOCKERFILE_PATH=deploy/Dockerfile.ingest`,
  empty start command (the image's `CMD` is `-from-targets -proxy-mode all
  -watch`, an always-on queue worker), restart policy **ALWAYS**, no cron, no
  domain, no healthcheck.
- **console** (optional): same repo, `RAILWAY_DOCKERFILE_PATH=deploy/Dockerfile.console`,
  healthcheck `/healthz`, a public domain, `API_BASE_URL` pointing at mcpd's
  public URL. No secrets in its env: the bearer token is typed at login and held
  in an http-only cookie.

### 2. Declare the variables

Use reference variables wherever a value comes from another service; the docs
call that out as what makes a template good quality. On **mcpd** and **crawler**:

| Variable | Template value | Deploy-time behaviour |
|---|---|---|
| `DATABASE_URL` | `${{Postgres.DATABASE_URL}}` | resolved automatically, private network |
| `STORAGE_BACKEND` | `s3` | fixed |
| `S3_ENDPOINT` | `${{versitygw.RAILWAY_PRIVATE_DOMAIN}}:9000` | private VersityGW address |
| `S3_BUCKET` | `thumbs` | fixed |
| `S3_ACCESS_KEY` | `${{versitygw.ROOT_ACCESS_KEY}}` | referenced |
| `S3_SECRET_KEY` | `${{versitygw.ROOT_SECRET_KEY}}` | referenced (never logged) |
| `S3_REGION` | `us-east-1` | fixed |
| `S3_USE_SSL` | `false` | fixed |
| `RAILWAY_DOCKERFILE_PATH` | `deploy/Dockerfile` (mcpd) / `deploy/Dockerfile.ingest` (crawler) | fixed |
| `MCP_BEARER_TOKEN` (mcpd) | `${{secret(48)}}` | generated per deploy |
| `PUBLIC_IMG_TOKEN` (mcpd) | `${{secret(48)}}` | generated per deploy; gates the `/img` photo proxy |
| `URL_TOKEN_AUTH` (mcpd) | `true` | lets URL-only connectors pass the token as `?token=` |
| `CONSOLE_URL` (mcpd) | `https://${{console.RAILWAY_PUBLIC_DOMAIN}}` | where `get_console_url` points |
| `ROOT_SECRET_KEY` (versitygw) | `${{secret(48)}}` | generated per deploy |
| `WEBSHARE_API_KEY` (crawler) | *(empty, prompted, optional)* | deployer sets a residential key |
| `PHOTO_TTL_DAYS` (crawler) | `30` | rolling thumbnail cache window; `0` disables the age sweep (delisted listings still drop their photos) |
| `API_BASE_URL` (console) | `https://${{mcpd.RAILWAY_PUBLIC_DOMAIN}}` | the REST API the console reads; keep it the public origin (or set `PUBLIC_BASE_URL` on mcpd) — photo URLs are minted from it |

**The deployer types ZERO secrets.** Railway documents exactly two template
variable functions: `${{secret(length?: number, alphabet?: string)}}` (32
chars by default) and `${{randomInt(min?: number, max?: number)}}`. Both are
evaluated at deploy time, with the result written into the deployed service.
Using `${{secret(48)}}` for both `MCP_BEARER_TOKEN` and `ROOT_SECRET_KEY` means
Railway generates strong values automatically, leaving `WEBSHARE_API_KEY` (only
needed for the *cloud* crawl) as the single, optional prompted variable.

Optionality and help text are **composer UI fields, not syntax**, set by hand
at composition time. Railway's best practices ask you to "always include a
description of what the variable is for", and for secrets to "use template
variable functions to generate them, avoid hardcoding default credentials."

**`WEBSHARE_API_KEY` lives on the *crawler* service with honest help text:**
prompted but effectively optional, with a description that says a
**residential** Webshare key makes the cloud crawler work; a datacenter/free key gets the API PX-blocked (the
[provider-access decision](../../docs/decisions.md)), in which case the
deployment still works (state + MCP) and you crawl from your own machine.
Overselling a datacenter plan is the failure mode to avoid.

### 3. Write the description and readme

The template's readme is what the marketplace shows, and it is also the first
support-load lever. Cover, briefly: that the one server serves MCP (root), the
REST API (`/v1`), and interactive docs (`/docs`) on a single URL with the same
auto-generated bearer token, so the deployer types no secrets; that the cloud
crawler needs a **residential** Webshare key and otherwise no-ops, with the
deployer's own machine as the always-works fallback (pointer to
`deploy/README.md` and this file); that photos accumulate in VersityGW on their own
once a crawl runs, and the server works with an empty bucket; that a **fresh
deploy starts with no crawl scope** and the model queues one when the user asks
(or set `SEED_CRAWL_AREAS` to pre-seed); and the exact `claude mcp add
--transport http … --header "Authorization: Bearer …"` line. Say plainly that this indexes StreetEasy and
that the deployer is responsible for how they use it.

### 4. Publish, then take the badge

Publish from the composer's publish button, Workspace **Settings → Templates →
Publish**, or the CLI:

```bash
railway templates publish <template-id> --category Other \
  --description "Self-hosted taste memory for your home search over MCP. Zero secrets to type." \
  --readme-file deploy/railway/TEMPLATE_README.md --json
```

The deploy page is `https://railway.com/new/template/<code>`; Railway's
documented badge form adds `?utm_medium=integration&utm_source=button&utm_campaign=<template-name>`
for attribution. Property Radar's badges point at a redirect,
`https://go.dteather.com/property-radar-template?src=<surface>&placement=<where>`,
which resolves to that URL and lets the maintainer see which page a click came
from. It sits in the root `README.md` (badge row and the Railway section) and
in `docs/deploying/railway.md`:

```md
[![Deploy on Railway](https://railway.com/button.svg)](https://go.dteather.com/property-radar-template?src=property-radar&placement=readme)
```

A fork publishes its own template and swaps the link for its own deploy page.

Two things worth turning on afterwards: the template **metrics** page
(deployments, support health) and **updatable templates**, which opens a PR
against deployed copies when this repo changes. That covers GitHub-sourced
services only, which is what ours is.

---

# Part C: what this actually costs

Hobby is **$5/month and includes $5 of usage**; you are billed the delta only
if usage exceeds it. Postgres is not a separate SKU; Railway databases are
ordinary services drawing on the same pool.

Published rates (2026-08-24): RAM **$10/GB/mo**, CPU **$20/vCPU/mo**, volume
storage **$0.15/GB/mo of storage used**, egress **$0.05/GB**. Private-network
traffic and inbound traffic are free.

Rough monthly estimate for this stack, always-on:

| Item | Assumption | ~$/mo |
|---|---|---|
| `mcpd` RAM | ~100 MB (static Go binary, distroless) | 1.00 |
| `mcpd` CPU | near-idle between MCP calls | 0.10–0.40 |
| Postgres RAM | ~250 MB idle | 2.50 |
| Postgres volume | ~1 GB of rows | 0.15 |
| VersityGW RAM | ~15 MB idle | 0.15–0.20 |
| VersityGW volume | 1–3 GB of thumbnails used | 0.15–0.45 |
| crawler | an idle Go worker (a few MB) plus short crawl passes | ~0.10 |
| Egress | photo bytes to the client, a few GB | 0.10–0.30 |
| **Total usage** | | **~$4.2–5.2** |

VersityGW keeps idle RAM near its ~15 MB binary floor (versus MinIO's ~150 MB
idle footprint, which is why the template uses it; see the
[photos & storage decision](../../docs/decisions.md)), so decoupling the
crawler from `mcpd` costs only a couple dimes a month, and a personal instance
stays under the included $5. If you never run the cloud crawler, Railway's native
`bucket()` resource avoids the storage container entirely (change
`STORAGE_BACKEND`/`S3_*` to point at it). The cheapest real
levers are keeping the bucket small (it is a cache; deleting it costs nothing
but re-crawling) and never using `DATABASE_PUBLIC_URL` for the crawl-time
firehose.

Set a usage limit while you learn the shape of the bill: Workspace → Usage, or
`railway usage limit set`.

### Serverless / app sleeping: leave it off

Railway still has app sleeping, renamed **Serverless** (service Settings →
Deploy → Serverless; `deploy.sleepApplication` in the old config format). For
this workload it is the wrong trade, for three documented reasons:

1. **Sleep is decided by outbound traffic**: "If no packets are sent from the
   service for over 10 minutes, the service is considered inactive," and
   "inbound traffic is excluded". `mcpd` holds a pgx pool against Postgres over
   the private network, and the docs list "open database connection pools" and
   private-network requests as things that keep a service awake. It would very
   likely never sleep, and you would pay the downsides for no saving.
2. **The first request to a slept service "may return a 502 Bad Gateway."** An
   MCP endpoint's first request is a tool call in the middle of someone's
   conversation. A cold-start delay is survivable; a 502 mid-conversation is a
   broken tool.
3. **The healthcheck cannot paper over it.** Railway does not probe the
   endpoint after deploy, so nothing keeps the service warm on your behalf.

Sleeping would save at most the ~$1.10/mo `mcpd` costs, and Postgres would keep
running regardless. Not worth it. Revisit only if Railway starts basing
inactivity on inbound traffic.

---

## Sources

Verified 2026-08-24.

- Config as Code (deprecation, 2026-12-01 cutoff): <https://docs.railway.com/config-as-code>
- Infrastructure as Code: <https://docs.railway.com/infrastructure-as-code> · reference: <https://docs.railway.com/infrastructure-as-code/reference>
- Dockerfiles / `RAILWAY_DOCKERFILE_PATH`: <https://docs.railway.com/builds/dockerfiles>
- Ports / "Application failed to respond": <https://docs.railway.com/networking/troubleshooting/application-failed-to-respond>
- Healthchecks: <https://docs.railway.com/guides/healthchecks> (canonical: <https://docs.railway.com/deployments/healthchecks>)
- Serverless (app sleeping): <https://docs.railway.com/deployments/serverless>
- Cost control: <https://docs.railway.com/pricing/cost-control>
- Plans and resource pricing: <https://docs.railway.com/reference/pricing/plans> · <https://railway.com/pricing>
- Cron / scheduled services (`cronSchedule`, 5-field crontab, 5-min minimum, exit-to-complete, no overlap): <https://docs.railway.com/reference/cron-jobs>
- Private networking (`*.railway.internal`, IPv6): <https://docs.railway.com/guides/private-networking> · <https://docs.railway.com/networking/private-networking>
- VersityGW (S3 gateway over a POSIX directory, S3 API on `:9000`, no console): <https://github.com/versity/versitygw>
- minio-go SDK (v7, `StatObject`/`PutObject`/`GetObject`): <https://github.com/minio/minio-go>
- Volumes reference (limits, billed on used storage, root-owned mounts): <https://docs.railway.com/reference/volumes> (canonical: <https://docs.railway.com/volumes>)
- Volumes guide (CLI file transfer): <https://docs.railway.com/guides/volumes>
- `railway volume` CLI: <https://docs.railway.com/cli/volume>
- `railway ssh`: <https://docs.railway.com/cli/ssh> · shell requirement in practice: <https://station.railway.com/questions/ssh-not-working-error-a-shell-is-requi-252b21ec>
- `railway connect` (`--tunnel-only`): <https://docs.railway.com/cli/connect>
- TCP proxy: <https://docs.railway.com/networking/tcp-proxy>
- Private networking: <https://docs.railway.com/networking/private-networking> · <https://docs.railway.com/networking/private-networking/how-it-works>
- Variables and reference syntax: <https://docs.railway.com/variables> · <https://docs.railway.com/variables/reference>
- Postgres service variables and TCP-proxy egress billing: <https://docs.railway.com/databases/postgresql>
- Databases are unmanaged services: <https://docs.railway.com/databases>
- Create a template (`secret()`, `randomInt()`): <https://docs.railway.com/templates/create>
- Deploying a template deploys the repo directly (no fork): <https://docs.railway.com/templates/deploy>
- Template best practices (variable descriptions): <https://docs.railway.com/templates/best-practices>
- Publish a template + deploy badge: <https://docs.railway.com/templates/publish-and-share>
- Template metrics and updates: <https://docs.railway.com/templates/metrics> · <https://docs.railway.com/templates/updates>
- `railway run`: <https://docs.railway.com/cli/run>

Every page on `docs.railway.com` is also served as markdown by appending `.md`
to the URL, and the whole corpus is at `https://docs.railway.com/llms-full.txt`,
which is useful when re-verifying any of this. The older `/guides/*` and
`/reference/*` paths are being retired; a few still resolve, most now 404.
