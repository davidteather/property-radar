# Property Radar

Self-hosted taste memory for your home search. Your AI forgets your apartment
hunt the moment the chat ends; this deploy remembers it: hard filters, every
verdict you give, a rubric the model writes about you, and the actual listing
photos, served to any MCP client. **The server never calls a model** — all
judgment stays with your LLM.

## What this deploys

| Service | Role |
|---|---|
| `mcpd` | One URL serving MCP (`/`), the REST API (`/v1`), interactive docs (`/docs`), and a copy-paste setup page (`/connect`) |
| `crawler` | Always-on queue worker that fills the corpus from the scopes you request |
| `console` | Small web UI: listings with photos, lists, verdicts and rubric, crawl status |
| Postgres | Your listings, verdicts, rubric |
| `versitygw` | S3-compatible thumbnail cache on a volume |

**You type zero secrets.** `MCP_BEARER_TOKEN`, `PUBLIC_IMG_TOKEN`, and the
storage key are generated per deploy. The one optional prompt is
`WEBSHARE_API_KEY` on the crawler (see below).

## After deploy

1. Open `https://<your mcpd domain>/connect` — it shows the exact client setup.
2. Connect Claude Code:
   ```bash
   claude mcp add property-radar --transport http https://<your mcpd domain> \
     --header "Authorization: Bearer <MCP_BEARER_TOKEN from the mcpd service variables>"
   ```
   claude.ai and other URL-only connectors can pass the token as `?token=…`.
3. Ask the model to call `get_state`. A fresh deploy has **no crawl scope**; the
   model offers to queue one (`request_crawl`, place names welcome). Or set
   `SEED_CRAWL_AREAS` on the crawler to pre-seed one standing scope.

## The crawler and the Webshare key

The corpus comes from StreetEasy, which blocks datacenter IPs. The cloud
crawler therefore needs a **residential** Webshare proxy key in
`WEBSHARE_API_KEY`; a free or datacenter plan gets blocked and the crawler
no-ops (the server and MCP tools still work). To get one:

1. Sign up at [Webshare](https://go.dteather.com/webshare?src=property-radar&placement=railway-template)
   and buy a **Residential** plan (Dashboard → Proxy → Plans).
2. Dashboard → **API** → **API Keys** → **Create API Key**, copy it.
3. On the `crawler` service here → **Variables** → paste it as
   `WEBSHARE_API_KEY`. The crawler redeploys on its own.

The always-works fallback is running the crawler from your own home connection
against this database; see `deploy/README.md` and `docs/deploying/railway.md`
in the repository.

Photos accumulate in `versitygw` on their own once a crawl runs; the server
works fine with an empty bucket.

## Responsible use

This indexes a third-party listing site for your own, personal search. You are
responsible for how your instance is used and exposed. Property Radar is free,
MIT-licensed, and not affiliated with StreetEasy.

Repository and docs: https://github.com/davidteather/property-radar
