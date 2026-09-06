# Property Radar decisions

This is the authoritative decision record for Property Radar: five standing
decisions that describe the system as it is today. Each states the decision and the reasoning that still matters; where a
choice was later revised, only the current reality is recorded, not the history.

## 1. Architecture & the no-LLM boundary

**Decision.** A four-layer system whose dependencies point at application-owned
domain types: provider adapter (`internal/streeteasy`) → ingest
(`internal/ingest`, the crawl/lifecycle use case) → store (`internal/store`,
Postgres) → thin MCP/REST transports. `internal/domain` is the lingua franca
(`Property`, `Verdict`, `Rubric`, `Profile`, `Money`, `Address`) with zero
external deps and no serialization tags; providers convert into it, transports
out of it. Implemented in Go, current stable (1.27), stdlib-first, as small
static binaries (`cmd/ingest`, `cmd/mcpd`, `cmd/lst`, plus the optional
`cmd/console` and REST-only `cmd/api`) whose composition roots only wire things
up.

**Why / current state.** The hard rule: **this repo never calls an LLM** — no
model SDKs, embeddings, vector DBs, ML ranking, or agent frameworks. The caller
(Claude/ChatGPT) owns all judgment, vision, and ranking; the repo owns
fetch/normalize/persist/filter/remember. "This change needs an LLM SDK" is a
design smell, and lint (`depguard` in `.golangci.yml`) rejects known model SDK
imports. Interfaces are
consumer-owned (each package defines the small interface it needs; mockery
generates mocks only for those, no central `interfaces/` package and no
pass-through service that would just forward). Provider-specific knowledge stays
quarantined in one adapter package per provider so drift is a parser fix, not a
rewrite. Generic helpers live only under a scoped `internal/shared/<name>` (e.g.
`ptr`, `xslices`), promoted when a second package needs them, never a
generically named `utils`/`helpers` grab-bag.

## 2. Persistence & domain model

**Decision.** Postgres is the source of truth (pgx/pgxpool, readable SQL, goose
migrations). Dual representation: a typed `listing_current` row per listing for
the hot filter path, plus raw provenance (`listing_sources.raw` JSONB +
per-field `listing_fields`) for explainability and reconciliation, never the
normal query path. Inserts are event-only: `price_events` only on an actual
price change, `material_changed_at` advances only for changes worth resurfacing.
Money is `domain.Money` as `int64` whole USD dollars behind a `Currency` enum
seam (`USD` only today).

**Why / current state.** All SQL is confined to `internal/store`: row structs
stay package-private, public methods speak `domain` types, and a missing query
means a new store method, never inline SQL elsewhere. Whole dollars match every
provider (cents never appear); the enum field keeps a future non-US expansion
open at near-zero cost while avoiding money-struct ceremony today. Testcontainers
store tests are the enforcement point for the data invariants (change-only
events, resurfacing, delist-after-2-missing, append-only verdicts/rubrics).
Absence is scope-scoped: a run only ages listings owned by its own crawl scope,
so a narrow filtered one-off can never delist what other scopes track. One-off
runs (`ingest_runs.one_off`) observe without taking ownership, so an overlapping
request_crawl cannot make a standing scope's listings un-delistable either; a
standing run adopts anything a one-off found first, and a standing scope that
covers a one-off's areas and filters ages what that one-off saw but it did not,
so one-off discoveries do not linger un-delistable.

## 3. Provider access & crawling

**Decision.** V0's single provider is StreetEasy, accessed inside
`internal/streeteasy` via its GraphQL search API for enumeration and
listing-page HTML (embedded JSON preferred over CSS selectors) for detail, both
behind wire types + `convert.go`. Politeness first: large pages (perPage 500),
~1.5s inter-request delay, concurrency 1. Proxying is a `-proxy-mode {off,split,all}`
selector over a Webshare pool.

**Why / current state.** StreetEasy PX-blocks datacenter IPs on the GraphQL API,
so a cloud host's own IP is blocked. Only **residential** proxies in `-proxy-mode all`
(both API and site pages through the pool) let a cloud crawl worker reach the
API; `off` is local/residential-laptop, `split` proxies only site pages. A
datacenter or free proxy plan is honestly a no-op rather than pretended to work;
a provider-level block is a run-level failure that cannot corrupt data or
mass-delist. The always-available fallback is running the crawler from the
operator's own machine against the same cloud DB and bucket. `mcpd` never
crawls. Test fixtures are synthetic — real-shaped payloads with fabricated
content (no real listing data or auth material); normal tests never touch the
network.

**Queue-worker crawling.** `crawl_targets` is the queue (Postgres, no broker)
and the deployed crawler is an always-on worker (`ingest -from-targets -watch`):
it drains due targets, then sleeps on a Postgres `crawl_due` LISTEN that
`request_crawl`'s insert NOTIFYs on commit, with a poll fallback. mcpd triggers
crawls without ever executing them, and agent requests run within moments.
Scheduling is two-speed: a drain runs each standing scope incrementally once
its last run is `-standing-every` (1h) old, and deep (full re-enrich) when the
last deep pass is ~20h stale; once targets always run deep, immediately. Settled once targets (the async job rows callers poll via
get_crawl_status) are pruned after about a week. A one-shot `-from-targets`
drain remains for laptops and one-shot deploys.

## 4. Photos, storage & HTTP transport

**Decision.** Photos are cached as content-addressed, write-once, ≤800px JPEG
thumbnails under key `{provider}/{2-hex-shard}/{sha256}.jpg`, behind a
consumer-owned `PhotoStore` (`local|s3`) seam so the crawler and `mcpd` share one
cache even on different machines. The S3 backend is **VersityGW**, a tiny
S3-over-POSIX gateway. `get_listing` shows photos as an **inline contact sheet by default** (so the
model can see them), with `individual`, `links` (public content-addressed
`resource_link` URLs: `/img/{key}` and the constructable `/img/l/{id}/{n}`), and
`none` as the other `photos` modes. Transport is a
stateless Streamable HTTP MCP behind a single static bearer token
(`MCP_BEARER_TOKEN`).

**Why / current state.** The content-addressed layout makes repeat crawls cost
~zero photo work; bytes are evicted when a photo is replaced, its listing is
delisted, or `PHOTO_TTL_DAYS` passes.
VersityGW replaced an earlier MinIO choice, which was dropped because its idle RAM
(auto-sized from host memory) dominated the bill for an idle single-user tool;
the S3 interface means photostore needs no code change. Serving links instead of
inline base64 removes the per-turn vision-token cost and matches what a host can
render directly; the sha256 key is the capability. The public `/img` proxy is
**optionally gated by `PUBLIC_IMG_TOKEN`**: when set, image URLs must carry
`?k=<token>`, a lesser, read-only capability than the bearer that the tools and
console append automatically, so galleries/artifacts keep working while the proxy
stops being an anonymous, enumerable rehost; unset leaves it public. On the
transport: `MCP_BEARER_TOKEN` is constant-time compared, `/healthz` is open, TLS
is the operator's job (Caddy or Tailscale). This deliberately takes the transport
half of hosting without the identity half. OAuth 2.1 and per-user state scoping
are explicitly deferred, and one token fronting one operator's corpus is the
honest scope until multi-user identity is built.

## 5. Testing & code style

**Decision.** Fixture-based decode/convert tests for the provider adapter,
testcontainers Postgres for the store/mcp/restapi suites, mockery for
consumer-owned interfaces, golangci-lint plus gofmt/vet. `make check` (fmt + vet
+ lint + test) is the merge bar for every deliverable; Docker is required for the
full suite and tests skip (not fail) without it. Comments are minimal.

**Why / current state.** Quality gates must be mechanical so every change,
human or agent-authored, clears the same bar, and normal tests must never
depend on the live provider network. A comment earns its place only by stating a non-obvious
invariant or reason the code cannot express (and stays short); routine doc
comments are omitted; this is an application, not a library. Durable
reasoning lives here in `docs/decisions.md`, not in code comments.
