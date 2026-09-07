# Property Radar

[![LinkedIn](https://img.shields.io/badge/LinkedIn-say%20hi-0077B5?style=flat-square&logo=linkedin&logoColor=white)](https://go.dteather.com/linkedin)
![Go 1.27](https://img.shields.io/badge/go-1.27-00ADD8?style=flat-square&logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/davidteather/property-radar?style=flat-square)](https://github.com/davidteather/property-radar/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/davidteather/property-radar.svg)](https://pkg.go.dev/github.com/davidteather/property-radar)
[![Sponsor](https://img.shields.io/badge/Sponsor-%E2%9D%A4-ea4aaa?style=flat-square&logo=githubsponsors&logoColor=white)](https://github.com/sponsors/davidteather)
![Visitors](https://visitor-badge.laobi.icu/badge?page_id=davidteather.property-radar)

[![Deploy on Railway](https://railway.com/button.svg)](https://go.dteather.com/property-radar-template?src=property-radar&placement=readme)

**Your AI forgets your home search the moment the chat ends. Property Radar
doesn't.**

It's a memory layer you host yourself. Listings get crawled from StreetEasy
into your own Postgres, and every reaction you give in chat sticks: love, maybe,
dislike, a taste rubric in your own words, lists like ⭐ favorites or "big
windows".

Plug Claude or ChatGPT in over MCP and a fresh conversation just carries on. It
knows what you've already seen, looks at the actual photos, and hands you the
three to five homes worth a look, with your own past reactions as the reasons.
Mention a neighborhood it hasn't covered and it queues the crawl. A small web
console shows the whole thing in a browser.

The twist: **this repo never calls a model.** No SDKs, no embeddings, no
ranking. It fetches, stores, filters, and serves photos. Taste, vision, and
judgment stay with whatever LLM you plugged in. That line is the whole design.

Free, MIT-licensed, and personal. You run your own copy; nothing is hosted for
anyone else and there's no paid tier.

> **Early days.** NYC only for now (StreetEasy). The provider is a swappable
> seam, so more metros and sources are on the
> [roadmap](#status--roadmap). Screenshots soon; the demo below shows the loop
> end to end.

https://github.com/user-attachments/assets/cba7e24e-088d-44d4-8360-fdb843768d1e

---

## Table of contents

- [What it does](#what-it-does)
- [Why it's interesting](#why-its-interesting)
- [Quickstart](#quickstart)
- [How it works](#how-it-works)
- [The tool surface](#the-tool-surface)
- [Getting listings in (the crawler)](#getting-listings-in-the-crawler)
- [Status & roadmap](#status--roadmap)
- [Documentation](#documentation)
- [Get in touch](#get-in-touch)
- [Responsible use](#responsible-use)
- [License](#license)

---

## What it does

You tell Property Radar your hard constraints once (price, beds, neighborhoods,
carrying cost). It keeps a canonical corpus of listings crawled from StreetEasy,
and it remembers four kinds of taste memory:

- **Verdicts**: an immutable log of `love` / `maybe` / `dislike` plus a note,
  one per listing you react to.
- **A rubric**: a short, versioned, *you-authored* summary of your taste that
  the caller model rewrites from the verdict history as it grows.
- **Lists**: a built-in ⭐ favorites bucket plus any feature list you name
  ("big windows", "great yards"), each with an optional emoji. The model files
  listings into them as you react.
- **Shown history**: what you've already been offered, so recommendations stay
  fresh until a listing materially changes.

Then, in a conversation, you say something like:

> "Show me 3–5 new homes you think I'd like. Look at the photos and the copy,
> use everything I've told you before, and tell me why each made the cut."

The caller model calls `get_state` to load your profile, rubric, and verdicts;
`get_candidates` to pull fresh matches; and `get_listing` to actually *look at*
the photos of the finalists. You react in plain language; the model turns that
into `record_verdict` calls. **The next conversation retrieves all of that over
MCP and does better, even a brand-new one with no chat memory.**

## Why it's interesting

- **Model-agnostic taste memory.** Your feedback lives in Postgres, not in a
  single chat's context. Any MCP-capable model can pick it up.
- **Caller-side multimodal judgment.** The model inspects real cached photos of
  your shortlist (vision is the magic) while the backend stays deterministic.
- **No lock-in, no model bill on the server.** The server is a boring, testable
  Go service. You bring the intelligence.
- **Two-stage, cost-aware photo handling.** Search returns compact text rows;
  only the handful you're judging pull image bytes. Building a gallery for many
  listings uses constructable public image URLs and costs zero extra tool calls.

<!-- ══════════════════════════════════════════════════════════════════════
     TODO (media) — SCREENSHOTS: drop images into docs/media/ and add a "Demo & screenshots"
     section here once they exist. Shots worth capturing:
       1. A fresh conversation making aligned recommendations with reasons.
       2. The interactive REST docs at /docs (Scalar UI).
       3. The /connect onboarding page.
       4. A generated photo gallery page built from constructable image URLs.
     ══════════════════════════════════════════════════════════════════════ -->

## Quickstart

Property Radar is one server (`mcpd`, serving MCP + REST + docs on a single
port) plus Postgres, an S3-compatible thumbnail store, and a crawler. Two
supported ways to run it, **the same four services either way:**

### Run it locally (free, ~2 minutes)

```bash
cp env.example .env
openssl rand -hex 32   # -> MCP_BEARER_TOKEN in .env
openssl rand -hex 32   # -> S3_SECRET_KEY   in .env
docker compose --profile stack up -d --build

curl -fsS http://localhost:8787/healthz            # -> ok
# then open http://localhost:8787/docs  and  http://localhost:8787/connect
```

Connect a client (`.env` is read by compose, not your shell, hence the export):

```bash
set -a; . ./.env; set +a
claude mcp add property-radar --transport http http://localhost:8787 \
  --header "Authorization: Bearer $MCP_BEARER_TOKEN"
```

Full walkthrough (including crawling in listings): **[docs/deploying/LOCAL.md](docs/deploying/LOCAL.md)**.

### Deploy to Railway (always-on HTTPS)

The cloud shape mirrors the local one exactly: Postgres, VersityGW, `mcpd`, and
an always-on crawl worker, behind one auto-generated bearer token and a
`https://<you>.up.railway.app` URL. The one-click template provisions all of it
with the secrets generated for you; the guide covers the CLI path too:
**[docs/deploying/railway.md](docs/deploying/railway.md)**.

[![Deploy on Railway](https://railway.com/button.svg)](https://go.dteather.com/property-radar-template?src=property-radar&placement=quickstart)

**Not sure which?** See the pros/cons table in
[docs/deploying/README.md](docs/deploying/README.md). Prefer your own VPS?
[deploy/README.md](deploy/README.md).

## How it works

```text
StreetEasy ──▶ crawler (ingest) ──▶ Postgres (canonical corpus + taste state)
                    │                     ▲
                    └▶ thumbnails ──▶ S3 store (VersityGW)
                                          │
                                    mcpd  ├─ MCP     at  /      (bearer token)
                                          ├─ REST    at  /v1
                                          └─ docs    at  /docs
                                          │
                                   Claude / ChatGPT  ◀── ALL judgment, vision, ranking
```

- **The crawler** (`cmd/ingest`) fetches listings, normalizes them into a typed
  canonical schema with full provenance, tracks price history and lifecycle, and
  caches capped ~800px thumbnails. It runs on a residential connection because
  the provider blocks datacenter IPs.
- **Postgres** is the source of truth: listings, price events, photos metadata,
  and the taste state (profile, verdicts, rubric, shown).
- **The thumbnail store** is S3-compatible object storage so the crawler and the
  server can run on different machines and share one content-addressed cache
  (no image bytes ever live in Postgres).
- **`mcpd`** is a thin, deterministic transport over a shared application layer.
  The same handlers serve MCP (stdio or streamable HTTP) and a REST API; it owns
  database migrations and serves the public photo proxy.
- **The console** (`cmd/console`, optional) is a tiny login-gated web UI over
  the REST API: browse listings and photos, manage lists, review verdicts and
  the rubric from a browser. No JavaScript build; ~12 MB image.

The architectural rules that keep this honest live in
[`docs/go_architecture_guidelines.md`](docs/go_architecture_guidelines.md) and
the consolidated decision record in [`docs/decisions.md`](docs/decisions.md). The original planning spec is
[`docs/property-radar-v0-spec.md`](docs/property-radar-v0-spec.md).

## The tool surface

The same capabilities are exposed as **MCP tools** (for LLM callers) and **REST
endpoints** (for everything else, documented interactively at `/docs`).

| MCP tool | REST | What it does |
|---|---|---|
| `get_state` | `GET /v1/state` | Profile + latest rubric + `rubric_stale` + full verdict history. Call first. |
| `search_listings` | `GET /v1/listings` | Ad-hoc hard-filter search (ignores saved profile); compact rows, paged, no photos. |
| `get_candidates` | `GET /v1/candidates` | Profile-matching listings that are unseen or materially changed and unjudged. |
| `get_listing` | `GET /v1/listings/{id}` | Full detail for one listing; the only tool that returns image bytes/links. |
| `get_listings_photos` | `POST /v1/listings/photos` | Bulk public photo URLs for up to 30 listings; pure DB read, for galleries. |
| `mark_shown` | `POST /v1/shown` | Record that listings were presented, so they stop resurfacing (pass the search's `queried_at` as `as_of` so a change landing mid-conversation still resurfaces). |
| `record_verdict` / `record_verdicts` | `POST /v1/verdicts` · `/verdicts/batch` | Append immutable love/maybe/dislike feedback. |
| `set_profile` | `PATCH /v1/profile` | Partial update of the single hard-filter profile. |
| `update_rubric` | `POST /v1/rubric` | Append a new taste-rubric version. |
| `request_crawl` | `POST /v1/crawl-requests` | Queue a crawl as an async job (returns a `job_id`); accepts plain place names or numeric area ids. Creates a recurring standing scope by default (`one_off` for a one-time snapshot). |
| `manage_crawl_target` | `PATCH` · `DELETE /v1/crawl-targets/{id}` | Pause, resume, make recurring, or delete a crawl scope. |
| `get_crawl_status` | `GET /v1/crawl-requests/{id}` | Poll an async crawl job: pending → running → done, with listing counts. |
| `get_corpus_stats` | `GET /v1/corpus` | Corpus-wide totals and last-crawl freshness; how a caller detects an empty install. |
| `list_crawl_targets` | `GET /v1/crawl-targets` | List crawl scopes and their status. |
| `resolve_areas` | `GET /v1/areas` | Resolve human place names ("Upper West Side", "the whole city") to crawl area ids. |
| `get_lists` / `get_list` | `GET /v1/lists` · `/v1/lists/{id}` | List the curated lists / one list's listings. |
| `create_list` | `POST /v1/lists` | Create a named feature list (optional emoji). |
| `add_to_list` / `remove_from_list` | `POST /v1/lists/{id}/items` · `DELETE /v1/lists/{id}/items/{listing_id}` | File a listing into / out of a list. |
| `get_console_url` | — | Hand the user a link to the web console, when one is configured. |
| `reset_state` | `POST /v1/reset` | Destructive: wipe all taste memory, lists, and crawl scopes (keeps the listings corpus). |

Photos also have proxy routes: `GET /img/{key}` (content-addressed) and
`GET /img/l/{id}/{n}` (constructable from a listing id + photo index, so it
embeds straight into an `<img>` tag). Set `PUBLIC_IMG_TOKEN` to gate them
behind a `?k=<token>` access key (a lesser, read-only capability than the
bearer, which the tools and console append to image URLs automatically) so your
deployment isn't an anonymous, enumerable rehost of provider photos; leave it
unset and the routes are fully public.

The thumbnail store is a **rolling cache, not an archive**: the crawler evicts a
listing's cached photos as soon as it's delisted, and sweeps out any photo not
re-verified within `PHOTO_TTL_DAYS` (default 30). So the cache holds only photos
of listings currently on the market, and expired bytes are deleted rather than
kept. Set `PHOTO_TTL_DAYS=0` to disable eviction if you want a permanent cache.

## Getting listings in (the crawler)

The server serves data; the crawler fills it. StreetEasy PX-blocks datacenter
IPs (see [Provider access & crawling](docs/decisions.md) in the decision
record), so the crawl must originate from a **residential** connection:

- **Locally**, your home IP already qualifies: the API works with no proxy, and
  a [Webshare](https://go.dteather.com/webshare?src=property-radar&placement=readme)
  key only helps fetch listing site pages through a residential pool.
- **In the cloud**, the datacenter IP is blocked, so the cloud crawler needs a
  **residential** Webshare plan, or you run the same crawl from your own machine
  against the cloud database. Either way `mcpd` never crawls.

Both paths take the same `WEBSHARE_API_KEY`, on the crawler only. Where to get
the key and where to paste it:
[Getting a Webshare key](docs/deploying/railway.md#getting-a-webshare-key).
Crawling from your machine: the [local walkthrough](docs/deploying/LOCAL.md).

## Status & roadmap

**v0: single-operator, one provider (StreetEasy), sale and rental listings.**
Runs locally or on Railway with remote HTTP MCP, an S3 thumbnail store, and a
queue-driven crawl worker (agent-requested crawls run within minutes; standing
scopes re-crawl about hourly and deep-refresh roughly daily).

Next up:

- [ ] Demo video and screenshots
- [ ] Re-cache listings whose photos failed to fetch
- [ ] Second provider + cross-provider matching

Out of scope for now: multi-user auth/tenancy, implicit taste signals,
embeddings or any ML ranking, and, as a hard rule, any LLM call from this
repository.

## Documentation

| Doc | What's in it |
|---|---|
| [docs/deploying/](docs/deploying/README.md) | How to run it: [local](docs/deploying/LOCAL.md), [Railway](docs/deploying/railway.md), pros/cons |
| [docs/first-run.md](docs/first-run.md) | Five-minute smoke test to confirm a fresh instance works |
| [deploy/README.md](deploy/README.md) | Self-host on a VPS (Caddy/Tailscale TLS) |
| [docs/decisions.md](docs/decisions.md) | Consolidated architecture decisions (persistence, photos, transport, storage…) |
| [docs/data-model.md](docs/data-model.md) | The Postgres schema and listing semantics |
| [docs/go_architecture_guidelines.md](docs/go_architecture_guidelines.md) | Normative code architecture rules |
| [docs/property-radar-v0-spec.md](docs/property-radar-v0-spec.md) | v0 design overview (historical) |
| [AGENTS.md](AGENTS.md) | Contributor/agent guide and contracts |

## Get in touch

I'd genuinely like to know what you're building with this — which metro you
pointed it at, how your AI's recommendations landed, what broke. It's the best
signal for what to work on next.

- ⭐ **Star the repo** if it's useful — it's the honest vote that keeps me going.
- 💬 **Tell me how you're using it** — a [LinkedIn](https://go.dteather.com/linkedin)
  message is the surest way to reach me. Hearing the actual use case makes my day.
- 🐛 **Hit a bug or want a feature?** Open an [issue](https://github.com/davidteather/property-radar/issues)
  or send a PR.
- ❤️ **[Sponsor the project](https://github.com/sponsors/davidteather)** if it
  saved you time, it keeps the open-source work going.

## Responsible use

Property Radar is a **free, personal**, self-hosted tool for one operator's own
home search — not a product, a hosted service, or a data broker. (That is the
intent it is built for, not a licence term; the MIT licence says what you may do.)
It accesses StreetEasy on your behalf; **you are responsible** for your use of it
and for complying with applicable law and StreetEasy's Terms of Service. The project **does not host, provide, or redistribute** any listing
data or images; everything is fetched and stored by the instance *you* run.
Crawl politely (the defaults rate-limit and cap the queue); don't run it as a
public data service or resell the data. Provided **as-is, without warranty**
(see [LICENSE](LICENSE)). Not affiliated with, endorsed by, or connected to
StreetEasy or Zillow.

## License

See [LICENSE](LICENSE).

<!-- This README is the initial public draft: prose is real, but the demo GIF,
     screenshots, badges, and the Railway template badge are still placeholders.
     Fill them in with the public-OSS milestone. -->
