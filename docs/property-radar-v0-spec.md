# Property Radar — v0 design overview

**Listings aggregation + persistent taste memory + caller-side LLM judgment**

> **Historical design overview.** This is a compact summary of the v0 design and
> the decisions that shaped it. It is not the authoritative reference — the live,
> canonical docs win wherever they differ:
> [`decisions.md`](decisions.md) (consolidated decisions of record),
> [`data-model.md`](data-model.md) (the schema and listing semantics in full),
> [`go_architecture_guidelines.md`](go_architecture_guidelines.md) (normative code
> rules), and the [README](../README.md) (product, tool surface, deploy).

## Product goal

Aggregate real-estate listings into one canonical corpus, persist explicit user
feedback and a compact taste rubric, and expose both over MCP so the caller's
frontier LLM can do the qualitative ranking and photo inspection — without this
service ever calling an LLM API. The backend fetches, normalizes, persists,
filters, remembers, and serves photos; the caller model inspects descriptions
and images, infers taste, ranks candidates, explains choices, and turns
natural-language feedback into structured writes.

The thesis it proves: persisted taste state (not conversational memory) makes a
brand-new model conversation give materially better-aligned recommendations.

## Locked architectural decisions

- **All subjective intelligence is caller-side.** No Anthropic/OpenAI SDK,
  embeddings, vector DB, ML ranking, vision service, or agent framework in this
  repository. Importing a model SDK here is a design smell. The backend is
  deterministic only: normalize, persist, filter, detect changes, track
  provenance, serve images/data.
- **Single-operator v0.** One instance = one profile/rubric + one corpus.
  Multi-user auth/tenancy/identity is deferred, not baked into the API.
- **One provider (StreetEasy) behind a replaceable adapter seam.** A second
  provider is the change that would earn dedup/matching work. Provider access,
  auth, and proxy mechanics stay isolated inside the provider package and config;
  no credentials/cookies/tokens are ever stored in raw listing payloads.
- **Local-first by default, with an optional hosted (Railway) shape.** `mcpd`
  never crawls; the crawler no-ops honestly on datacenter IPs (residential
  connection required). Remote Streamable HTTP MCP (bearer token) and
  S3-compatible thumbnail storage ship as options, not a separate product.
- **Explicit feedback only:** `love | maybe | dislike` plus a free-text note. No
  click/view/dwell inference.
- **One profile for hard constraints; one append-only, versioned rubric for
  taste; raw verdict history stays authoritative.** The rubric is a
  caller-maintained summary of taste, never the ground truth.
- **Go monorepo, Postgres as the source of truth**, `pgx` with plain SQL, a small
  migration runner, `log/slog` structured logs. Provider fixtures + unit tests +
  store integration tests against local Postgres; no live-provider calls in
  normal tests.

## Layering

```text
provider network → 1. provider client + adapter   (fetch • normalize; no taste, no MCP, no LLM)
                 → 2. canon + store                (canonical listings, provenance, lifecycle, photos, taste state)
                 → 3. MCP + REST service           (thin, deterministic tool surface over the store)
                 → 4. Claude / ChatGPT             (ALL vision, ranking, and taste judgment; caller owns model usage)
```

The ingest worker owns provider calls. The MCP/REST service owns only
database/file reads and writes. The full tool surface and its REST mirror are
documented in the [README](../README.md#the-tool-surface) and served
interactively at `/docs`.

## Schema & listing semantics (gist)

Full schema, indexes, and rationale live in [`data-model.md`](data-model.md).
The shape at a glance:

- **`listings`** is canonical identity + lifecycle (`active` / `in_contract` /
  `sold` / `rented` / `delisted`, plus `material_changed_at`). **`listing_current`** is the
  typed hot read model (price, beds, baths, sqft, carrying costs, description),
  so filters never scan JSONB. **`listing_sources`** + **`listing_fields`** keep
  raw provider payloads and per-field provenance for explainability.
- **`price_events`** is append-only, written *only* on an actual canonical price
  change. **`listing_photos`** records photo positions and cached thumbnail
  metadata.
- Taste state: **`verdicts`** (immutable; the latest verdict for a listing is the
  current opinion), **`rubric_versions`** (append-only; `through_verdict_id` makes
  staleness measurable), **`profile`** (single-row hard filters), **`shown`** (the
  version a listing was last presented at, so a later material change can
  resurface it).
- **`ingest_runs`** is the durable operational record; scoped volume invariants +
  lifecycle guardrails mean a bad or partial crawl can never mass-delist the
  corpus (delisting requires repeated absence across complete, non-suspect runs
  for the same scope, or an explicit provider status).

## Explicitly out of scope for v0

- Second provider and cross-provider matcher/dedup.
- Multi-user auth, OAuth, tenancy, hosted accounts, billing, or a full dashboard.
- Implicit taste signals (clicked, viewed, ignored, dwell time).
- Embeddings, vector DB, ML ranking, a custom recommendation model, backend
  vision, or any LLM API call.
- Full-resolution image archiving (only capped thumbnails are cached).
- A general-purpose scraping framework or anti-bot platform.
- Perfect address/entity resolution.
