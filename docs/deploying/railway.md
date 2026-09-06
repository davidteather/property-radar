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

<!-- ┌─────────────────────────────────────────────────────────────────────┐
     │ TEMPLATE — TO BE PUBLISHED                                           │
     │ The one-click Railway template is not published yet (it's gated on   │
     │ the public-OSS milestone; the repo must be public first). When it     │
     │ ships, fill in the blanks below. Publishing steps are in Part B of    │
     │ deploy/railway/README.md.                                            │
     └─────────────────────────────────────────────────────────────────────┘ -->

<!-- TEMPLATE BADGE: paste the published badge markdown here once the template
     is live:
[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template/XXXXXX?utm_medium=integration&utm_source=button&utm_campaign=property-radar)
-->

Deploying the template gives you every service pre-wired, with
`MCP_BEARER_TOKEN` and the storage secret **auto-generated** — you type no
secrets. What the deployer does:

1. Click **Deploy on Railway** and confirm the project.
2. _(Optional)_ paste a **residential** `WEBSHARE_API_KEY` on the `crawler`
   service to enable the always-on cloud crawler. Skip it to crawl from your own
   machine instead; everything else works without it.
3. Wait for the build, then **Generate Domain** on the `mcpd` service.
4. Copy `MCP_BEARER_TOKEN` from the `mcpd` service's **Variables** tab (or
   `railway variables --service mcpd`) and register the endpoint with your
   client (see [Connect a client](#connect-a-client)).

<!-- A screenshot or GIF of the deploy flow goes here once the template is published. -->

That's the intended 0-click experience. Until the template is live, use path B.

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

## What it costs

Roughly **~$1–6/month** all-in for an always-on personal instance, dominated by
memory; the switch to VersityGW keeps the thumbnail store near its ~15 MB idle
floor. The detailed line-item breakdown and the "leave Serverless off" reasoning
are in
[runbook Part C](../../deploy/railway/README.md#part-c-what-this-actually-costs).
Set a usage cap while you learn the shape of the bill: Workspace → Usage.

## Publishing the one-click template

<!-- ┌─────────────────────────────────────────────────────────────────────┐
     │ TO BE FILLED IN when the template is published. The full procedure   │
     │ (compose from the working project, declare reference/secret vars,     │
     │ write the marketplace readme, publish, take the badge) is in          │
     │ Part B of deploy/railway/README.md. Summarize the deployer-facing     │
     │ result here and paste the badge into the "One-click template"         │
     │ section above and the root README.                                    │
     └─────────────────────────────────────────────────────────────────────┘ -->

The template is composed from a working project and published from the Railway
dashboard. Step-by-step:
[Part B of the runbook](../../deploy/railway/README.md#part-b-publish-it-as-a-template).
Gated on making the repo public (the template deploys this repo directly, so it
must be reachable).

## See also

- [`deploy/railway/README.md`](../../deploy/railway/README.md): the full runbook (authoritative)
- [`.railway/railway.ts`](../../.railway/railway.ts): Infrastructure as Code for the project
- [LOCAL.md](LOCAL.md): the same stack on your machine
- [Deploying overview](README.md): pros/cons of each path
