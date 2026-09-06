<!-- Keep it short. Delete lines that don't apply. -->

## What & why

<!-- One or two sentences. Link the issue if there is one. -->

## Checklist

- [ ] `make ci` passes locally (fmt + vet + lint + test)
- [ ] No new LLM/model calls or provider-network calls added to this repo (the [no-LLM boundary](https://github.com/davidteather/property-radar/blob/main/docs/decisions.md))
- [ ] SQL stays in `internal/store`; public methods speak `domain` types ([persistence decision](https://github.com/davidteather/property-radar/blob/main/docs/decisions.md))
- [ ] Comments stay minimal — only non-obvious invariants/reasons ([code-style decision](https://github.com/davidteather/property-radar/blob/main/docs/decisions.md))
- [ ] Tests added/updated (fixtures sanitized, no real scraped data committed)
- [ ] Docs updated if behavior changed (README / AGENTS.md / `docs/decisions.md`)
