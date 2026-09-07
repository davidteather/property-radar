# Deploying Property Radar

Property Radar is one server (`mcpd`) that serves **MCP at the root, the REST API
at `/v1`, and interactive docs at `/docs`** on a single port behind one bearer
token, plus a **Postgres** corpus, an **S3-compatible thumbnail store**, and a
**crawler** that populates them. Every deploy target runs those same four
pieces; they only differ in where they run and who operates them.

There are two supported paths, and they are deliberately **the same shape**:

| | [**Local**](LOCAL.md) | [**Railway**](railway.md) |
|---|---|---|
| Runs on | your machine, via Docker Compose | Railway's cloud, one project |
| Services | `db`, `storage`, `mcpd`, `crawler` | Postgres, versitygw, mcpd, crawler |
| Thumbnail store | VersityGW (S3) on a local volume | VersityGW (S3) on a Railway volume |
| Reachable from | `http://localhost:8787` | `https://<you>.up.railway.app` |
| TLS | none (loopback) | automatic |
| Cost | free | ~$1–6/mo (see [railway.md](railway.md)) |
| Setup | `cp env.example .env` → `docker compose --profile stack up` | click Deploy (template) or the CLI runbook |
| Best for | trying it, developing, a private single-box instance | a persistent instance reachable by hosted clients |

The local compose (`docker-compose.yml`) **mirrors the Railway services
one-to-one** on purpose: the same VersityGW thumbnail store, the same env-var
wiring, the same crawler binary. What works locally works the same way in the
cloud, and the docs stay in sync.

## Which should I use?

**Use Local if** you want zero cost and zero accounts, you're developing or
evaluating, or you only ever call the server from a desktop MCP client (Claude
Code, Claude Desktop) on the same machine. Trade-off: it's only reachable at
`localhost`, so hosted web connectors (claude.ai, ChatGPT) can't see it, and it's
up only while your machine and Docker are.

**Use Railway if** you want a persistent HTTPS endpoint that's always on and
reachable from anywhere, with TLS and an always-on crawl worker handled for you, and
you're fine spending a few dollars a month. Trade-off: it needs a Railway
account and a card, and the *cloud* crawl needs a residential Webshare plan
(the fallback is crawling from your own machine into the cloud database).

### The crawler caveat applies to both

StreetEasy PX-blocks datacenter IPs, a
[provider-access decision](../decisions.md). So the crawler must reach the
provider from a **residential** connection:

- **Local:** your home IP already is residential — the API works with no proxy
  at all; a Webshare key only helps with site pages.
- **Railway:** the datacenter IP is blocked, so the cloud crawler needs a
  **residential Webshare** plan, or you run the crawl from your own machine
  against the Railway database. Either way, `mcpd` never crawls and serves
  whatever the corpus already holds.

Both systems take the **same `WEBSHARE_API_KEY`**; it lives on the crawler only.

## The other path: a plain VPS

Prefer your own box (Hetzner, a Linode, a Pi)? The
[VPS runbook](../../deploy/README.md) covers a Docker Compose install with a
split crawl-on-laptop / serve-on-VPS topology, TLS via Caddy or Tailscale, and
SSH-tunnelled migrations. It predates the mirrored local compose and uses a
local-disk thumbnail cache rather than the S3 store; it's the right choice when
you want full control of the host.

## Environment reference

Every variable any binary reads. `mcpd`, `api`, and `ingest` share the `config`
loader; `console` reads only its own three.

| Variable | Read by | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | mcpd, api, ingest, lst | local dev Postgres | canonical corpus; `postgres://` scheme required |
| `MCP_BEARER_TOKEN` | mcpd, api | — | client credential; required for HTTP transports, optional on stdio; warned if under 16 chars |
| `PUBLIC_IMG_TOKEN` | mcpd, api | empty (proxy open, warned) | `?k=` capability gating the public `/img` photo proxy; appended to minted URLs. Set it before going public: while open, images are served `Cache-Control: public` for a year, so a CDN or shared cache in front keeps copies after you later gate the proxy |
| `PUBLIC_BASE_URL` | mcpd, api | derived per request | absolute origin for `/img` links and the connect page; required for usable photo links on stdio (warned when unset). Set it explicitly on any public deployment: the derived origin trusts the edge's `Host`/`X-Forwarded-*` headers, so a misconfigured proxy would otherwise mint links to whatever host the request claimed |
| `URL_TOKEN_AUTH` | mcpd | `false` | also accept `?token=` for URL-only web connectors |
| `CONSOLE_URL` | mcpd | empty | where `get_console_url` points; must be an `http(s)://host` origin, anything else is ignored with a warning |
| `PORT` | mcpd, api, console | — | PaaS-injected listen port; an explicit `-addr` wins |
| `MIGRATE_ON_START` | mcpd | `true` | apply embedded migrations before serving |
| `SEED_CRAWL_AREAS` | mcpd | empty | comma-separated area ids to pre-seed one standing sale scope on a fresh install |
| `PHOTO_TTL_DAYS` | ingest | `30` | rolling thumbnail cache window; `0` disables eviction; at most `3650` |
| `STORAGE_BACKEND` | mcpd, api, ingest, lst | `local`, or `s3` when `S3_BUCKET`+`S3_ENDPOINT` are set | thumbnail bytes backend |
| `THUMBS_DIR` | local backend | `~/.local/share/property-radar/thumbs` | thumbnail directory |
| `S3_ENDPOINT` `S3_BUCKET` `S3_ACCESS_KEY` `S3_SECRET_KEY` | s3 backend (incl. `lst doctor`) | — | all four required for `s3`; the endpoint is `host:port` with no scheme (`S3_USE_SSL` picks https) |
| `S3_REGION` / `S3_USE_SSL` | s3 backend | `us-east-1` / `true` | |
| `WEBSHARE_API_KEY` | ingest | empty (direct fetch) | residential proxy pool for the crawler |
| `API_BASE_URL` | console | — | required; the `mcpd`/`api` origin the console calls. Use the browser-reachable origin (or set `PUBLIC_BASE_URL` on the API): photo URLs are minted from it |
| `CONSOLE_READONLY` | console | `false` | `true` hides and disables writes (lists, crawl scopes, reset) |
| `STREETEASY_LIVE` | tests only | — | `1` enables the opt-in live provider smoke test |

## See also

- [Run it locally](LOCAL.md): the whole stack on your machine
- [Deploy to Railway](railway.md): the cloud path — one-click template or CLI, plus getting a Webshare key
- [Self-host on a VPS](../../deploy/README.md): your own box
- [Decision record](../decisions.md): the reasoning behind this shape, including
  the object store, the remote transport, and the cloud crawler
