# Deploy Property Radar on Railway

Railway runs the same services as the [local stack](LOCAL.md) on one
private network: Postgres, a VersityGW S3 thumbnail store, `mcpd`, a
queue-driven crawl worker, and the optional web console. You get an always-on **`https://<you>.up.railway.app`** endpoint
with TLS handled for you. One server, one URL, one token: MCP at the root, the
REST API at `/v1`, interactive docs at `/docs`.

> This page is the overview and the one-click template. **The full,
> step-by-step CLI runbook** lives in
> [`deploy/railway/README.md`](../../deploy/railway/README.md): creating each
> service, every variable, migrations, crawl setup, cost breakdown, and
> publishing the template. Start there for anything this page summarizes.

## Two ways in

### A. One-click template (0-click setup)

[![Deploy on Railway](https://railway.com/button.svg)](https://go.dteather.com/property-radar-template?src=property-radar&placement=docs)

Deploying the template gives you every service pre-wired, with
`MCP_BEARER_TOKEN`, `PUBLIC_IMG_TOKEN`, and the storage secret
**auto-generated** — you type no secrets. What the deployer does:

1. Click **Deploy on Railway** and confirm the project.
2. _(Optional)_ paste a **residential** `WEBSHARE_API_KEY` on the `crawler`
   service to enable the always-on cloud crawler
   ([how to get one](#getting-a-webshare-key)). Skip it to crawl from your own
   machine instead; everything else works without it.
3. Wait for the five services to build; `mcpd` and `console` get public
   domains automatically. Open `https://<mcpd domain>/connect`.
4. Copy `MCP_BEARER_TOKEN` from the `mcpd` service's **Variables** tab (or
   `railway variables --service mcpd`) and register the endpoint with your
   client (see [Connect a client](#connect-a-client)).

<!-- A screenshot or GIF of the deploy flow goes here. -->

Prefer a terminal, or want to change the shape before the first deploy? Path B.

### B. From the CLI / dashboard

Follow **[Part A of the Railway runbook](../../deploy/railway/README.md#part-a-deploy-your-own-instance)**.
In short: create a Postgres service, add `mcpd` and `crawler` from this repo
(each pointed at its Dockerfile via `RAILWAY_DOCKERFILE_PATH`), add the VersityGW
storage service, set the variables below, generate a domain. The repo also ships
[`.railway/railway.ts`](../../.railway/railway.ts) (Infrastructure as Code) that
declares the whole project; `railway config apply` provisions it. Secrets are
declared `preserve()` there, so on a brand-new project set `MCP_BEARER_TOKEN`
(mcpd refuses to start without it), `PUBLIC_IMG_TOKEN`, and `WEBSHARE_API_KEY`
in the dashboard after the first apply.

## The services

| Service | Image / source | Role |
|---|---|---|
| **Postgres** | Railway Postgres | canonical corpus + taste state |
| **versitygw** | `deploy/versitygw/Dockerfile` | S3 thumbnail store on a volume ([why object storage](../decisions.md)) |
| **mcpd** | `deploy/Dockerfile` | MCP + REST + docs; owns migrations |
| **crawler** | `deploy/Dockerfile.ingest` | always-on queue worker; drains crawl requests within moments, deep-refreshes standing scopes ~daily |
| **console** | `deploy/Dockerfile.console` | optional htmx web UI over the REST API (`API_BASE_URL` → mcpd); mcpd's `CONSOLE_URL` points `get_console_url` at it |

## The variables

Set on **both** `mcpd` and `crawler` (reference variables resolve on the private
network):

| Variable | Value | Notes |
|---|---|---|
| `DATABASE_URL` | `${{Postgres.DATABASE_URL}}` | private URL, free internal traffic |
| `STORAGE_BACKEND` | `s3` | read/write thumbnails in VersityGW |
| `S3_ENDPOINT` | `${{versitygw.RAILWAY_PRIVATE_DOMAIN}}:9000` | private address |
| `S3_BUCKET` | `thumbs` | created at startup by whichever service connects first |
| `S3_ACCESS_KEY` | `${{versitygw.ROOT_ACCESS_KEY}}` | referenced |
| `S3_SECRET_KEY` | `${{versitygw.ROOT_SECRET_KEY}}` | referenced, never logged |
| `S3_REGION` / `S3_USE_SSL` | `us-east-1` / `false` | internal HTTP |
| `RAILWAY_DOCKERFILE_PATH` | `deploy/Dockerfile` (mcpd) · `deploy/Dockerfile.ingest` (crawler) | which image to build |

Only on `mcpd`:

| Variable | Value | Notes |
|---|---|---|
| `MCP_BEARER_TOKEN` | `openssl rand -hex 32` (template: `${{secret(48)}}`) | required; the client credential |
| `PUBLIC_IMG_TOKEN` | `openssl rand -hex 32` (template: `${{secret(48)}}`) | recommended; gates the public `/img` photo proxy so it isn't an anonymous, enumerable rehost of provider photos. The tools/console append `?k=<this>` to image URLs automatically, so galleries and artifacts keep working; a lesser read-only capability than the bearer, safe to embed in `<img src>`. Empty = proxy fully public |
| `URL_TOKEN_AUTH` | `true` | also accept the token as `?token=` for URL-only web connectors |
| `CONSOLE_URL` | `https://${{console.RAILWAY_PUBLIC_DOMAIN}}` | optional; where `get_console_url` sends people |

Only on `crawler`:

| Variable | Value | Notes |
|---|---|---|
| `WEBSHARE_API_KEY` | your **residential** Webshare key | optional; enables the always-on cloud crawler |
| `PHOTO_TTL_DAYS` | `30` | rolling thumbnail cache window; `0` disables eviction |

Only on `console`: `API_BASE_URL` (`https://${{mcpd.RAILWAY_PUBLIC_DOMAIN}}`), plus
`CONSOLE_READONLY=true` for a view-only UI.

`mcpd` never crawls, so it needs no `WEBSHARE_API_KEY`. It auto-applies
migrations on startup (`MIGRATE_ON_START` defaults true), so there is no
migration step.

<a name="connect-a-client"></a>
## Connect a client

`MCP_BEARER_TOKEN` is the value on the `mcpd` service's Variables tab
(`railway variables --service mcpd` prints it too):

```bash
claude mcp add property-radar --transport http https://<you>.up.railway.app \
  --header "Authorization: Bearer $MCP_BEARER_TOKEN"
```

Add `--scope user` for every project. Verify with `claude mcp list`, then ask
the model to call `get_state`. A fresh deploy starts with an **empty corpus**:
just tell the model what you're after ("2-beds under $2M in Manhattan") and it
queues the crawl for you. Prefer a preset? Set `SEED_CRAWL_AREAS` on `mcpd` to
comma-separated area ids. Photos accumulate in the bucket once a crawl runs.

> **claude.ai / ChatGPT web connectors:** these need the URL form of the token.
> With `URL_TOKEN_AUTH=true` the server accepts `?token=<MCP_BEARER_TOKEN>` on
> the URL. Full app-directory OAuth is out of scope — see
> [Photos, storage & HTTP transport](../decisions.md).

## Crawling in the cloud

The `crawler` service is an always-on worker running
`ingest -from-targets -proxy-mode all -watch`: it drains due crawl targets, then
sleeps on a Postgres `crawl_due` notification (with a poll fallback), so an
agent's `request_crawl` typically runs within moments. Standing scopes re-crawl
about hourly and do a deep re-enrich roughly daily. Idle cost is a
sleeping Go process.
`-proxy-mode all` routes both the StreetEasy API and site pages through the
Webshare pool, which reaches the API **only with a residential plan**. A
datacenter/free key gets PX-blocked (the [provider-access decision](../decisions.md))
and the run harmlessly no-ops. The always-works fallback is running the same
crawl **from your own machine** against the Railway Postgres + bucket over
`railway connect --tunnel-only`; see
[runbook step 10](../../deploy/railway/README.md#10-running-the-crawl).

### Getting a Webshare key

1. Create an account at [Webshare](https://go.dteather.com/webshare?src=property-radar&placement=railway-guide).
2. Buy a **Residential** proxy plan (Dashboard → **Proxy** → **Plans**). The
   free plan and the datacenter plans are the ones StreetEasy blocks; the
   cloud crawler will no-op on them, so size the plan to what you'll crawl and
   skip the rest.
3. Dashboard → **API** → **API Keys**
   ([dashboard.webshare.io/userapi/keys](https://dashboard.webshare.io/userapi/keys))
   → **Create API Key**, then copy it. Every Webshare key has full account
   access, so treat it like a password: it goes in Railway only, never in git.
4. In Railway, open the `crawler` service → **Variables** → set
   `WEBSHARE_API_KEY` to the key. Railway redeploys the crawler on save.
5. Ask your client to `request_crawl` a neighborhood, then `get_crawl_status`
   on the returned job. The crawler's logs end each pass with a
   `crawl targets drained` line; `listings_seen > 0` means the pool is working.

Only the `crawler` service takes the key. `mcpd` and the console never crawl.

## What it costs

Roughly **~$1–6/month** all-in for an always-on personal instance, dominated by
memory; the switch to VersityGW keeps the thumbnail store near its ~15 MB idle
floor. The detailed line-item breakdown and the "leave Serverless off" reasoning
are in
[runbook Part C](../../deploy/railway/README.md#part-c-what-this-actually-costs).
Set a usage cap while you learn the shape of the bill: Workspace → Usage.

## How the one-click template is maintained

The template (code `57e20w`) is a recipe stored on Railway, independent of any
running project: five services, each sourced from this repository's `main`
branch, with every literal from [`.railway/railway.ts`](../../.railway/railway.ts)
typed in as a default and the secrets declared as `${{secret(48)}}`. Code
changes flow to new deploys on their own (they build from `main`); only a change
to the service *shape* — a new variable, service, or volume — needs an edit in
the template composer. The full procedure, from composing to publishing:
[Part B of the runbook](../../deploy/railway/README.md#part-b-publish-it-as-a-template).

## See also

- [`deploy/railway/README.md`](../../deploy/railway/README.md): the full runbook (authoritative)
- [`.railway/railway.ts`](../../.railway/railway.ts): Infrastructure as Code for the project
- [LOCAL.md](LOCAL.md): the same stack on your machine
- [Deploying overview](README.md): pros/cons of each path
