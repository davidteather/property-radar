# Run Property Radar locally

Run the whole thing on your own machine with Docker Compose: the same four
services the [Railway deploy](railway.md) uses (Postgres, a VersityGW S3
thumbnail store, `mcpd`, and the crawler), wired the same way, so local
behaviour matches the cloud. No account, no cost, reachable at
`http://localhost:8787`.

> **Reachability note.** A local server lives at `localhost`, so **desktop MCP
> clients** (Claude Code, Claude Desktop) can use it, but **hosted web
> connectors** (claude.ai, ChatGPT) cannot, since they can't reach your machine.
> Want an always-on HTTPS endpoint hosted clients can see? Use
> [Railway](railway.md).

## Prerequisites

- **Docker** with the Compose plugin (`docker compose version`).
- That's it. Postgres, the thumbnail store, and the Go binaries all run in
  containers, so there's nothing else to install.

## 1. Configure

From the repo root:

```bash
cp env.example .env
openssl rand -hex 32    # paste as MCP_BEARER_TOKEN in .env
openssl rand -hex 32    # paste as S3_SECRET_KEY in .env
```

`.env` is gitignored. Leave `WEBSHARE_API_KEY` empty for now. You only need it
to crawl (step 4), and from a home connection the StreetEasy API works without
it.

## 2. Start the server

```bash
docker compose --profile stack up -d --build
```

That builds and starts three services: `db` (Postgres), `storage` (VersityGW,
the S3 thumbnail bucket), and `mcpd`. `mcpd` applies its database migrations on
startup and serves once Postgres is healthy.

Verify (`docker compose ps` should show all three `running`; a service stuck
restarting almost always means step 1 was skipped, and `docker compose logs
mcpd` or `storage` names the missing variable):

```bash
docker compose ps
curl -fsS  http://localhost:8787/healthz            # -> ok   (open, no token)
curl -si   http://localhost:8787/v1/state | head -1 # -> HTTP/1.1 401  (needs token)
```

Now open the interactive docs and the onboarding page in a browser:

- **<http://localhost:8787/docs>** — the full REST API reference (Scalar UI)
- **<http://localhost:8787/connect>** — copy-paste client setup

`db` is published on `127.0.0.1:5432` and `mcpd` on `127.0.0.1:8787`, loopback
only. `mcpd` speaks plaintext HTTP with a bearer token, so **don't expose port
8787 off the machine** without a TLS proxy in front of it; that's what the
[Railway](railway.md) and [VPS](../../deploy/README.md) paths are for.

## 3. Connect a client

Point Claude Code at the local server. `.env` is read by compose, not your
shell, so export it first (or paste the token in place of `$MCP_BEARER_TOKEN`):

```bash
set -a; . ./.env; set +a
claude mcp add property-radar --transport http http://localhost:8787 \
  --header "Authorization: Bearer $MCP_BEARER_TOKEN"
```

Add `--scope user` to make it available in every project. Verify with
`claude mcp list`, then ask the model to call `get_state`. On a fresh install
the corpus is empty, so the model will offer to queue a crawl (`request_crawl`);
run it in the next step.

**Claude Desktop** can't send a bearer header to a local HTTP server, so run a
second `mcpd` on stdio against the same database and the same thumbnail store
(`make binaries` first; `PUBLIC_BASE_URL` makes its photo links resolve through
the http one, `PUBLIC_IMG_TOKEN` must match it or the links it mints lack the
`?k=` the proxy demands, and the `S3_*` values point it at the compose store,
which is published on loopback port 9000 — without them it would look in an
empty local directory and report every photo as uncached):

```json
{
  "mcpServers": {
    "property-radar": {
      "command": "/absolute/path/to/property-radar/bin/mcpd",
      "env": {
        "DATABASE_URL": "postgres://property_radar:property_radar@localhost:5432/property_radar?sslmode=disable",
        "PUBLIC_BASE_URL": "http://localhost:8787",
        "PUBLIC_IMG_TOKEN": "<the PUBLIC_IMG_TOKEN from your .env, if you set one>",
        "STORAGE_BACKEND": "s3",
        "S3_ENDPOINT": "localhost:9000",
        "S3_BUCKET": "thumbs",
        "S3_ACCESS_KEY": "radar",
        "S3_SECRET_KEY": "<the S3_SECRET_KEY from your .env>",
        "S3_REGION": "us-east-1",
        "S3_USE_SSL": "false"
      }
    }
  }
}
```

That goes in `claude_desktop_config.json` (Settings → Developer → Edit Config);
restart Claude Desktop afterwards.

## 4. Crawl some listings

The server serves data; the crawler fills it. From a home (residential) IP the
StreetEasy API works directly, so a Webshare key is optional here; it only
helps fetch listing **site pages** through a residential pool. To use one, set
`WEBSHARE_API_KEY` in `.env` ([get a key](https://go.dteather.com/webshare));
otherwise leave it blank.

**Seed a scope first.** The crawler crawls what the `crawl_targets` table says
to, and a fresh install starts **empty** on purpose. Queue a scope the way the
model does (`request_crawl`, place names welcome), or set `SEED_CRAWL_AREAS` in
`.env` to comma-separated area ids to pre-seed one standing sale scope on first
boot. Confirm the queue with:

```bash
curl -s http://localhost:8787/v1/crawl-targets \
  -H "Authorization: Bearer $MCP_BEARER_TOKEN" | jq
```

**Run a one-shot crawl.** The `crawler` service runs the same binary the Railway
worker uses, as a one-shot: it drains the due targets and exits:

```bash
docker compose --profile crawl run --rm crawler
```

This drains every due target with `-proxy-mode ${CRAWL_PROXY_MODE:-off}` (direct
from your IP by default; set `CRAWL_PROXY_MODE=split` in `.env` to route site
pages via the Webshare pool), writes canonical rows into `db`, and
caches thumbnails into the shared `storage` bucket, the same one `mcpd`
reads, so photos show up immediately. Re-run it any time; thumbnails are
write-once, so repeat crawls only fetch what's new.

To crawl a specific area instead of the seeded scopes, override the command:

```bash
docker compose --profile crawl run --rm crawler -areas 305,304 -listing-type sale
```

(Exactly one of `-areas` and `-from-targets` is required; see the
[ingest flags](#crawler-flags) below.)

Ask the model for candidates again; it now has listings to work with.

## 5. Everyday commands

```bash
# Follow the server logs (one line per request; the token is never logged)
docker compose logs -f mcpd

# Stop the server (keeps data)
docker compose --profile stack down

# Start it again
docker compose --profile stack up -d

# Rebuild after pulling new code
docker compose --profile stack up -d --build

# Wipe everything, including the database and thumbnails
docker compose --profile stack down -v
```

Inspect the corpus from the host with the `lst` CLI (it reads the same
published Postgres):

```bash
make binaries      # builds bin/{lst,ingest,mcpd,api,console}
DATABASE_URL="postgres://property_radar:property_radar@localhost:5432/property_radar?sslmode=disable" \
  bin/lst status   # provider health, last runs, active count
```

## Just the database (for development)

If you're hacking on the Go code rather than hosting, you usually want only
Postgres and to run `mcpd` from your shell:

```bash
make db-up         # docker compose up -d db  (just Postgres)
DATABASE_URL=... go run ./cmd/mcpd
make db-down
```

That runs `mcpd` on stdio, which has no listener to mint photo links against:
without `PUBLIC_BASE_URL` the tools return relative `/img/...` paths (mcpd warns
at startup) that no client can load. Set `PUBLIC_BASE_URL` to the origin of an
http `mcpd` on the same database, or just use `-transport http` locally (which
also needs `MCP_BEARER_TOKEN` set; any string of 16+ characters will do here).

`make test` does not use this database: the store, MCP, and REST suites start
their own throwaway Postgres through testcontainers (Docker is all they need),
and skip rather than fail when Docker is absent.

The `stack` and `crawl` profiles keep the server/crawler out of the way until
you ask for them, so `make db-up` still starts Postgres alone.

## How this maps to Railway

Everything here has a one-to-one cloud counterpart, which is why the two guides
stay consistent:

| Local (compose service) | Railway (service) | Notes |
|---|---|---|
| `db` | Postgres | same schema, same migrations |
| `storage` (VersityGW) | versitygw | same S3 thumbnail store ([why it's object storage](../decisions.md)) |
| `mcpd` | mcpd | same image, same env wiring, `STORAGE_BACKEND=s3` |
| `crawler` (on demand) | crawler (always-on queue worker) | same binary; local runs it by hand |
| `.env` | Railway variables | `MCP_BEARER_TOKEN`, `S3_SECRET_KEY`, `WEBSHARE_API_KEY` |

Moving from local to cloud is a change of *where*, not *what*. See
[railway.md](railway.md).

## Troubleshooting

- **`mcpd` restarts a few times on first boot.** It waits for Postgres to pass
  its healthcheck and for `storage` to accept the bucket; `restart:
  unless-stopped` retries until both are ready. Give it a few seconds.
- **401 on every call.** The `Authorization: Bearer <token>` header is missing
  or the token doesn't match `MCP_BEARER_TOKEN` in `.env`. Only `/healthz`,
  the docs shell (`/docs`, `/openapi.*`, `/schemas/*`), `/connect`, and the
  `/img/*` photo proxy are open.
- **Photos don't appear.** They exist only after a crawl caches them. A listing
  can also legitimately show `photos_cached: 0` if the crawl failed to fetch its
  images; the server reports that rather than serving dead URLs.
- **Crawl returns nothing.** Either the scope has no matching listings, or from
  a blocked network the provider refused. Check `bin/lst status`. The crawler
  never mass-deletes on a bad run: a single failed crawl can't wipe the corpus.

<a name="crawler-flags"></a>
## Crawler flags

`docker compose --profile crawl run --rm crawler <flags>` passes flags straight
to `ingest`:

| Flag | Default | Meaning |
|---|---|---|
| `-from-targets` | (set in compose) | Crawl every due `crawl_targets` row. Don't point a laptop drain at a database a cloud worker also drains: a run that sleeps (lid closed) past the 2h stale window gets re-claimed by the worker, and both crawl the scope once. |
| `-areas` | — | Comma-separated StreetEasy area ids for an ad-hoc scope. Exactly one of `-areas` / `-from-targets` is required. |
| `-standing` | off | Treat the `-areas` run as a standing scope that owns absence (may delist listings it no longer sees). Off = a one-off that only adds and updates (with `-areas`). |
| `-listing-type` | `sale` | `sale` or `rent` (with `-areas`). |
| `-max-price` | `0` | Max sale price; `0` = no cap (with `-areas`). |
| `-min-beds` | `0` | Minimum bedrooms; `0` = none (with `-areas`). |
| `-proxy-mode` | `off` (compose: `$CRAWL_PROXY_MODE`) | `off` / `split` (API direct, pages via pool) / `all` (both via pool). `all` needs a residential key. |
| `-delay` | `1500ms` | Pause between provider requests. |
| `-timeout` | `2h` | Overall crawl timeout. A target left `running` longer than `max(-timeout, 2h)` is treated as abandoned and recovered at the next drain. |

See also the [deploying overview](README.md), the [Railway guide](railway.md),
and the [VPS runbook](../../deploy/README.md).
