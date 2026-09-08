# AGENTS.md

## Project: Property Radar

Property Radar is an open-source, single-tenant real-estate listings MCP server: each operator runs their own instance (locally or on Railway). It aggregates provider listings into a canonical corpus, persists explicit user taste and hard filters, and exposes deterministic tools to a caller such as Claude or ChatGPT.

The caller LLM is the intelligence layer. This repository is the data, memory, and tool layer.

Before making architectural changes, read `docs/decisions.md` (the consolidated, authoritative decision record) and `docs/go_architecture_guidelines.md` (normative code rules); `docs/data-model.md` covers the schema. `docs/property-radar-v0-spec.md` is a short historical design overview for context, not a live spec.

**Precedence:** `docs/go_architecture_guidelines.md` is the normative Go architecture/coding convention document. Where it conflicts with layout or package details in this file or the spec (e.g. the spec's top-level `listing/` and `streeteasy/` packages), the guidelines win. `docs/decisions.md` holds the consolidated architecture decisions (five thematic sections). Read it for how the system hooks together and before re-litigating any settled decision; record new architectural decisions there.

## Product goal

The target experience is:

> Every week, inspect newly available homes, look at the listing copy and photos, use everything I have previously said I like or dislike, and show me the 3–5 places I would most likely want to see.

The user then gives natural-language feedback. The caller LLM converts that feedback into structured MCP writes. A later, fresh conversation should make better recommendations using only state retrieved from Property Radar.

The core v0 proof is **persistent taste memory across conversations**, not merely successful scraping or an MCP server that responds.

## Current phase

V0 is deliberately constrained:

- Local-first and single-operator.
- Open source (MIT), but each instance is single-tenant: one operator, one profile, one bearer token.
- One provider: StreetEasy.
- Sale and rental listings (rentals added by operator decision 2026-08-23 for the renters-guide demo; the v0 exit test still runs on the sale corpus).
- One hard-filter profile.
- Explicit feedback only: `love | maybe | dislike` plus free-text note.
- One append-only rubric derived by the caller from verdict history.
- Postgres as the source of truth.
- MCP over stdio for local use, or Streamable HTTP behind one static bearer token when hosted.
- Ingest, Postgres, MCP server, and CLI run on the operator's machine or on one small PaaS deployment.
- Photos are part of the v0 recommendation loop.

Do not expand the phase merely because a later feature is obvious.

## Non-negotiable architecture boundary

**There are no model calls in this repository.**

Do not add:

- Anthropic SDKs.
- OpenAI SDKs.
- Backend calls to Claude, GPT, or another model.
- Embeddings or vector databases.
- A custom recommendation model.
- A backend vision model.
- LangChain, agent frameworks, or equivalent orchestration layers.

The caller LLM owns:

- Qualitative ranking.
- Photo inspection.
- Interpretation of listing descriptions.
- Taste inference.
- Tradeoff analysis.
- Recommendation explanations.
- Turning natural-language user feedback into `record_verdict` calls.
- Regenerating the taste rubric from verdict history.

Property Radar owns only deterministic application behavior: fetch, normalize, persist, reconcile, filter, detect changes, track provenance, serve images/data, and remember explicit state.

If a change appears to require importing an LLM SDK, treat that as an architectural warning and revisit the design.

## Layer ownership

```text
Provider network / raw data
        │
        ▼
1. PROVIDER CLIENT + ADAPTER
   - provider-specific network I/O
   - pagination, retries, rate limiting, proxy configuration
   - provider-native -> canonical mapping
        │
        ▼
2. CANON + STORE
   - canonical listings
   - raw source provenance
   - current typed fields
   - lifecycle + material changes
   - price history
   - profile, verdicts, rubric, shown state
   - cached thumbnails
        │
        ▼
3. MCP SERVICE
   - thin deterministic tools
   - reads/writes database and cached files
   - normally makes no provider network requests
        │
        ▼
4. CLAUDE / CHATGPT / OTHER CALLER
   - all subjective intelligence
   - all model inference
```

Keep these boundaries visible in package design and tests.

## Provider boundary

Following the provider-boundary rules in [`docs/go_architecture_guidelines.md`](docs/go_architecture_guidelines.md), the StreetEasy integration is one provider adapter package, `internal/streeteasy`, that owns its transport (`client.go`), unexported wire types (`wire.go`), and StreetEasy→domain conversion (`convert.go`). It implements the consumer-owned `ingest.Source` interface. Do not split a separately importable raw client preemptively; that split happens only if the raw client becomes independently valuable.

Desired direction:

```text
StreetEasy GraphQL/HTML
    ↓ unexported wire types (internal/streeteasy/wire.go)
internal/streeteasy convert.go
    ↓ ingest.SourceProperty (domain.Property + opaque raw provenance)
internal/ingest (application workflow)
    ↓ via consumer-owned store interface
internal/store → Postgres
```

Nothing downstream of the adapter should need to know StreetEasy response shapes.

Provider auth, cookies, proxy configuration, rate limiting, and retry logic belong in the provider integration. Never persist credentials, session tokens, cookies, or proxy secrets inside raw listing JSON.

## Ingestion boundary

The ingest worker owns provider calls. The MCP server should normally query already-ingested state rather than fetching StreetEasy synchronously for a tool request.

The intended flow is:

```text
provider.Search
    ↓
validate required identity/data
    ↓
store raw provider record
    ↓
canonicalize
    ↓
update typed current snapshot + provenance
    ↓
record material events
    ↓
prepare capped thumbnails
    ↓
finish ingest run + health checks
```

Use a transaction per listing or a small batch. Do not hold one transaction open for an entire crawl.

An individual malformed listing is normally a soft item error. Provider/pagination failures and suspiciously incomplete crawls are run-level failures.

## Data model invariants

Preserve these invariants even if schema details evolve:

1. **Raw source data is retained.** Canonicalization must not destroy the ability to inspect/reconcile the provider payload later.
2. **Hot query fields are typed.** Price, beds, baths, sqft, carrying costs, DOM, and description should not require generic JSONB/EAV scans for normal MCP searches.
3. **Canonical provenance is retained.** When a canonical field comes from a provider, keep enough provenance to explain where it came from.
4. **Price history records changes, not observations.** Insert a price event only when the canonical price actually changes.
5. **Material changes are explicit.** `material_changed_at` changes only for events worth resurfacing, initially price or status changes.
6. **One missing crawl does not mean delisted.** Missing-state advancement is allowed only after complete, non-suspect, non-empty comparable runs, and only within the scope that stopped seeing the listing (so `delisted` includes "re-priced outside a capped scope"; the next sighting by any scope reactivates it). Prefer explicit provider status when available.
7. **Shown state is version-aware.** A listing may resurface after a material change even if it was shown earlier.
8. **Verdicts are immutable.** A changed opinion appends another verdict. The latest row is current; history remains evidence.
9. **Rubrics are summaries, not ground truth.** Raw verdicts are authoritative. Rubric versions are append-only and identify the latest verdict they summarize.
10. **Single-provider v0 has no matcher.** Do not build cross-provider entity resolution until a real second-provider dataset exists.

## Canonical model scope

Avoid modeling every possible real-estate attribute.

Promote a field into typed canonical storage when it is needed for:

- hard filtering,
- compact candidate presentation,
- material-change tracking, or
- a core recommendation fact.

Keep provider-specific or rarely used attributes in raw/provenance storage until they earn first-class treatment.

Sale-side v0 should treat co-op/condo economics as first-class, including maintenance/common charges and monthly taxes when available.

## Photo contract

Photos are part of the product proof, not decorative metadata.

Use two-stage retrieval:

1. Candidate/search tools return compact textual metadata and no full photo sets.
2. `get_listing(id)` attaches photos only for shortlisted listings.

`get_listing` takes one `photos` mode (see [Photos, storage & HTTP transport](docs/decisions.md)):

- `contact_sheet` (default): one inline image grid the model can see; `max_photos` defaults to 12 (cap 30).
- `individual`: one inline image block per photo for close inspection; `max_photos` defaults to 6 (cap 10).
- `links`: each readable cached thumbnail as an MCP `resource_link` whose `uri` is a public image URL (`{origin}/img/{cached_path}`) the client fetches itself; shown to the user, not visible to the model.
- `none`: data only.
- The REST `GET /v1/listings/{id}` detail carries a `photos[]` array of `{position, hosted_image_uri, mime}` with the same public URLs, plus a `photo_sheets[]` plan whose 1-based `sheet` indices resolve on `/v1/listings/{id}/contact-sheet?sheet=N`; the bearer-gated `/v1/listings/{id}/photos/{n}` binary endpoint stays for authenticated raw fetches.
- The public image proxy `GET /img/{cached_path}` takes no bearer: the object is served straight from the (possibly private) photo store. The unguessable content-addressed `cached_path` (sha256) is one capability; when `PUBLIC_IMG_TOKEN` is set the proxy also requires `?k=<token>` (minted into every URL the server hands out, surfaced to the model as `image_access_key`), and when it is unset the server logs a warning at startup. Keys are validated to the `provider/shard/sha256.jpg` shape before the store is touched, and served with a long immutable cache lifetime. It is the only public face of an otherwise-private object-store bucket.

**Constructable URLs for galleries** (same decision). Building a photo gallery/website for *many* listings must not require one `get_listing` per listing:

- Every photo URL is **constructable** from `(listing id, position)`: `GET /img/l/{id}/{n}` (public, same `?k=` gating; a short 5-minute cache, since position `n` can point at a new photo after a re-crawl) resolves the listing's photo at 0-based position `n` to its cached thumbnail and serves the bytes; it re-validates the resolved content-addressed key before touching the store. A missing listing/position/thumbnail or a bad `id`/`n` is a 404.
- Compact `search_listings` / `get_candidates` rows (and REST `/v1/listings`, `/v1/candidates`) carry `photo_count` (total) and `photos_cached` (serveable). A caller builds a listing's gallery as `{base}/img/l/{id}/{n}` for `n` in `0..photos_cached-1` with **zero** extra calls.
- Bulk: MCP `get_listings_photos(ids)` (cap 30) and REST `POST /v1/listings/photos` return per listing `{id, photo_count, photos_cached, image_uris:[…]}`, the `/img/l/{id}/{n}` URLs for every cached photo, in one call, no image bytes. Prefer this over looping `get_listing`.
- `get_listing` is the **visual-inspection** tool for a shortlist, not a URL harvester. Reserve it for the handful being judged for taste.
- **Enumerability (accepted):** `/img/l/{id}/{n}` is enumerable by `(id, index)`, unlike the unguessable sha256 key. For a single-user tool serving public real-estate thumbnails this is acceptable and is the operator's explicit intent; the content-addressed `/img/{key}` route remains for unguessable URLs.

For v0:

- Retain provider source image URLs as provenance; never hotlink them — always serve our own cached copies.
- During ingestion, cache only capped thumbnails, approximately 640px on the longest edge (default 6 per listing, fetched by a small concurrent worker pool).
- Do not archive full-resolution originals.
- `get_listing` defaults to a contact sheet of up to 12 photos (`max_photos` caps at 30); the `individual` layout defaults to 6 (cap 10).
- Photo fetch/resize failures are observable but do not fail the listing ingest.
- The MCP/REST request path reads cached bytes (or just stats them for links); it never contacts the provider to satisfy a photo request.

## MCP contract

The MCP layer is thin and deterministic. Tool bodies should primarily be queries, inserts, or updates over stored state.

The tool surface is the 23 tools listed in the README; the ones with contract subtleties worth knowing:

- `get_state(verdicts_limit=300)`
  - profile,
  - latest rubric,
  - rubric freshness,
  - rubric `through_verdict_id`,
  - the most recent verdicts (oldest first) plus `verdicts_total` and `latest_verdict_id`,
  - `image_base_url` when the public origin is known.

- `search_listings(...)`
  - active canonical listings,
  - hard-filter overrides,
  - compact rows only (each carries `photo_count` / `photos_cached` for constructable gallery URLs),
  - `unmatched_neighborhoods` for filter names the corpus has never seen (also on `set_profile`),
  - hard result cap.

- `get_candidates(...)`
  - profile-filtered candidates,
  - unseen or materially changed,
  - always excludes already-verdict'ed listings (use `search_listings` to revisit them),
  - compact rows only (each carries `photo_count` / `photos_cached`).

- `get_listing(id, photos="contact_sheet", max_photos=0)`
  - full canonical detail (in `structuredContent`), a short human summary text block always present,
  - description,
  - provenance summary,
  - price history,
  - capped cached photos as an inline contact sheet by default, individual inline images, `resource_link` public URLs, or none, per `photos`,
  - photo metadata: `photo_count` / `photos_cached` / `photos_returned` / `photo_missing` (genuine failures only),
  - the per-listing visual-inspection tool; for a many-listing gallery use `get_listings_photos` or construct `/img/l/{id}/{n}` from the compact rows instead.

- `get_listings_photos(ids)`
  - bulk gallery photo URLs for up to 30 listings in one call,
  - per listing: `photo_count`, `photos_cached`, and `image_uris` (constructable `{base}/img/l/{id}/{n}` public JPEG URLs, one per cached photo),
  - pure DB read: no image bytes, no per-listing `get_listing`.

- `mark_shown(ids, as_of?)`
  - separate from candidate retrieval so interrupted recommendation runs do not silently lose candidates.

- `record_verdict(listing_id, verdict, note?)`
  - append-only explicit feedback.

- `set_profile(...)`
  - hard filters only; no inferred taste.

- `update_rubric(content, through_verdict_id)`
  - append-only caller-generated taste summary.

Prefer structured MCP results for machine-readable state. Do not hide recommendation heuristics in MCP tool handlers.

## Monitoring invariants

Provider drift is expected. Quiet degradation is more dangerous than loud failure.

Every ingest run should persist operational state including:

- provider,
- scope identity/hash,
- start/finish time,
- completeness,
- listing volume,
- created/updated counts,
- item errors,
- photo failures,
- suspect status.

V0 drift guardrail:

- Compare volume only against recent clean, complete runs for the same scope.
- Do not establish the baseline until at least three comparable clean runs exist.
- Mark a run suspect if observed volume falls below roughly 30% of the recent baseline.
- A suspect or incomplete run must not cause mass lifecycle transitions.

`lst status` should make provider health understandable without inspecting the database manually.

## Repository structure

Target layout:

```text
property-radar/
├── internal/
│   ├── domain/                 # canonical application-owned types (no external deps)
│   ├── ingest/                 # application/use-case: crawl workflow, lifecycle,
│   │                           #   monitoring, thumbnails; owns Source/Store interfaces
│   ├── listings/               # read/curation use cases shared by mcp, restapi, lst
│   ├── streeteasy/             # provider adapter: client.go, wire.go, convert.go
│   ├── store/                  # concrete Postgres persistence; rows + SQL private
│   ├── photostore/             # thumbnail bytes: local dir or S3 behind one seam
│   ├── config/                 # env -> validated config for the cmd/ roots
│   ├── mcp/                    # MCP transport adapter: server, tools, DTOs, convert
│   ├── restapi/                # huma REST adapter + /docs + public /img proxy
│   ├── bearerauth/             # bearer middleware + request logging
│   ├── publicurl/              # per-request origin for minting absolute /img URLs
│   ├── pgtest/                 # testcontainers Postgres for store tests
│   └── shared/                 # brand, logging, ptr, xslices helpers
├── cmd/
│   ├── ingest/                 # crawl worker (one-shot, -from-targets, or -watch)
│   ├── mcpd/                   # MCP server (stdio or http) + REST on one port; migrates
│   ├── api/                    # REST-only binary (no Dockerfile or deploy target uses it; mcpd serves REST)
│   ├── console/                # server-rendered web console over the REST API
│   └── lst/                    # cobra CLI: list, show, status, doctor
├── migrations/
├── testdata/streeteasy/        # synthetic provider fixtures + endpoint notes
├── docs/                       # decisions, data model, tool contract
├── deploy/                     # Dockerfiles, env.example, VPS + Railway runbooks
├── .railway/                   # Railway IaC (railway.ts)
├── docker-compose.yml
├── Makefile
├── .mockery.yml
├── AGENTS.md
└── README.md
```

Address normalization lives in `internal/domain` (it is an application concept); there is no separate `canon` package. Do not create separate repositories for the provider client and MCP service during v0.

## Technology defaults

Unless the spec changes, use:

- Go, current stable release (1.27).
- Standard library first.
- Official `github.com/modelcontextprotocol/go-sdk` for MCP.
- `pgx` / `pgxpool` for Postgres.
- Direct, readable SQL rather than a heavy ORM.
- `goose` for migrations.
- `cobra` for the `lst` CLI (operator choice).
- `testcontainers-go` (Postgres module) for store integration tests.
- `mockery` v3, pinned as a Go tool dependency; generate mocks only for
  consumer-owned interfaces listed in `.mockery.yml` (`make gen-code`).
- `golang.org/x/image` for thumbnail resizing and WebP decode.
- `log/slog` for structured logs.
- Docker Compose for local Postgres.
- Checked-in, synthetic provider fixtures (fabricated content) for deterministic provider tests.

Avoid dependencies that merely wrap small amounts of straightforward standard-library code.

## Testing rules

Normal automated tests must not depend on the live provider network.

Expected test layers:

- Provider parsing against checked-in StreetEasy fixtures.
- Canonicalization/unit normalization tests.
- Store integration tests against disposable/local test Postgres.
- Lifecycle tests for missing runs, material changes, price events, and resurfacing.
- Monitoring tests for suspect-volume behavior.
- MCP handler tests against a store/test database, not a live provider.
- Photo resize/cap/error-path tests.

A live provider smoke command/test may exist, but it must be opt-in and clearly separated from the normal suite.

Before completing a meaningful Go change, normally run:

```bash
gofmt -w <changed-go-files>
go test ./...
```

Use `go vet ./...` when the repository is sufficiently complete for it to be useful.

## Implementation style

- Prefer boring, inspectable code over frameworks.
- Keep network mechanics, canonicalization, persistence, and MCP handlers separable.
- Make failure semantics explicit.
- Wrap errors with operation/context while preserving the original error.
- Thread `context.Context` through network/database boundaries.
- Keep provider-specific vocabulary out of canonical and MCP packages.
- Keep SQL readable and migrations explicit.
- Do not create abstractions for hypothetical provider #2 behavior unless needed to maintain an already-decided seam.
- When a simple enum/string is enough for v0, do not build an extensible registry prematurely.
- Preserve raw evidence before attempting clever normalization.

## Scope guardrails

Do **not** add any of the following before the v0 fresh-conversation test passes unless required to unblock that test:

- Zillow, Apartments.com, or another second provider.
- Cross-provider matching/deduplication.
- Multi-user identity or OAuth.
- Multi-user hosted service (single-operator remote hosting and Streamable
  HTTP MCP with a bearer token shipped by operator decision; see
  [Photos, storage & HTTP transport](docs/decisions.md)).
- Scheduled cloud digests.
- Billing.
- Multiple saved searches/profiles.
- Implicit user signals.
- Semantic/vector retrieval.
- Model-side ranking inside this repository.
- Full-resolution image/object storage.
- General-purpose anti-bot/scraping infrastructure.

A future feature being easy is not sufficient reason to put it in v0.

## Build order

Prefer this sequence unless real implementation evidence requires changing it:

1. Canonical domain types, config, migrations, local Postgres.
2. StreetEasy raw client using fixtures.
3. Internal StreetEasy adapter and store.
4. Ingest worker with lifecycle safeguards, monitoring, and thumbnail preparation.
5. `lst` CLI for data inspection and provider health.
6. Transport-independent MCP handlers.
7. `mcpd` (stdio and Streamable HTTP) using the official Go MCP SDK.
8. Fresh-conversation product demo.
9. Only after the demo passes: public OSS polish and hosted work.

## V0 exit criterion

V0 is done when all of the following are true:

1. A fresh database can ingest roughly 250–500 real sale listings.
2. One hard-filter profile can be configured.
3. A caller model can retrieve candidates and inspect photos for finalists through local MCP.
4. The user can provide natural-language feedback on roughly ten listings and the caller can persist explicit verdicts.
5. The caller can regenerate and persist a rubric through the latest verdict.
6. A fresh conversation with no relevant conversational memory retrieves state only through MCP.
7. That fresh conversation produces noticeably better-aligned recommendations using persisted evidence, including visual preferences.
8. The repository has no LLM SDK dependency and makes no LLM API call.
9. Provider/parser failure cannot mass-delist listings from one bad run.
10. Materially changed listings can resurface after being shown.

Passing tests without demonstrating the improved fresh-conversation loop does not complete v0.

## Direction after v0

Already shipped by operator decision: remote Streamable HTTP MCP behind a
bearer token, S3-backed thumbnails, the optional web console, and rentals. The
architecture should leave seams for, but must not prematurely implement:

- Multi-user identity (OAuth) and user-scoped profile/verdict/rubric/shown state.
- Centralized/shared listing ingestion.
- Scheduled weekly recommendation tasks in the caller platform.
- Additional providers and actual cross-provider matching.

The product should continue to avoid owning LLM inference. Claude/ChatGPT/other callers should remain replaceable intelligence layers.

## Decision hierarchy for agents

When implementation choices conflict, prioritize in this order:

1. Preserve the no-backend-LLM boundary.
2. Preserve data/provenance and correctness.
3. Make the fresh-conversation v0 proof work.
4. Keep provider-specific code isolated.
5. Keep the MCP surface simple and deterministic.
6. Keep operations observable enough to detect silent provider drift.
7. Prefer the smallest implementation that does not block the hosted end state.
8. Optimize elegance/generalization only after the above are satisfied.

When uncertain, choose the narrower v0 implementation and leave a short comment or issue describing the earned extension point rather than implementing the extension now.
