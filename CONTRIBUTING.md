# Contributing

Property Radar is a local-first, single-operator real-estate taste-memory tool.
The backend is deterministic; all judgment stays with the caller LLM. Keep
changes small and boring. This file is the short version; `AGENTS.md` and
`docs/decisions.md` are the full contracts.

## Prerequisites

- Go 1.27
- Docker (for the store/integration tests via testcontainers)
- [golangci-lint](https://golangci-lint.run/) v2 (`make lint`; CI pins v2.13)

## Build & test

```bash
make check   # fmt + vet + lint + test — the merge bar
make ci      # what CI runs
go test -short ./...   # skips container-backed integration tests
```

Docker-less runs skip (not fail) the integration suites. CI runs `make ci`; a
green `make check` locally is the gate.

## Architectural boundaries you must honor

1. **This repo never calls an LLM** — the [no-LLM boundary](docs/decisions.md).
   No model SDKs, embeddings, vector DBs, ML ranking, or agent frameworks. If a
   change seems to need one, the design is wrong; open an issue instead.
2. **Layered architecture; all SQL lives in `internal/store`.** Row structs stay
   package-private; public store methods speak `internal/domain` types. A missing
   query is a new store method, never inline SQL elsewhere; see
   [Persistence & domain model](docs/decisions.md).
3. **Provider-agnostic domain types.** Provider adapters are quarantined; the
   reference adapter is `internal/streeteasy`. Nothing downstream should know a
   provider's response shapes; see [Provider access & crawling](docs/decisions.md).
4. **Comments stay minimal**, per the [code-style decision](docs/decisions.md).
   A comment earns its place only by stating a non-obvious invariant or reason
   the code can't express.

## Adding a new data source / provider

A new listing platform is a **new adapter**, not a rewrite:

- Implement the provider adapter against the same seam as `internal/streeteasy`
  (the consumer-owned `ingest.Source` interface): own its transport, wire types,
  and provider→domain conversion inside the adapter package.
- Keep the canonical `internal/domain` types unchanged; convert into them.
- Add **synthetic** fixtures: real-shaped payloads with fabricated content.
  **No auth material and no real scraped listing data or photos are committed.**
- Cross-provider dedup/matching is future work; don't build entity
  resolution into an adapter (see [Provider access & crawling](docs/decisions.md)).

## Issues & PRs

- File bugs and feature requests with the issue forms. Architecture/"how it
  works" questions belong in the docs links on the issue chooser, not as bugs.
- Keep PRs focused. Fill in the PR checklist; make sure `make ci` is green.

## CI gate

CI runs `make ci` (`.github/workflows/ci.yml`). Integration tests use
testcontainers, so CI has Docker. Match the gate locally before you push.
