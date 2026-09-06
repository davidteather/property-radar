# Data model

A human-readable tour of what Property Radar stores. The authoritative schema is
the goose SQL in [`migrations/`](../migrations/); this doc explains what each
table is *for* and how they relate. Two groups: the **corpus** (crawled listing
data) and the **taste state** (your feedback). Postgres is the source of truth;
no image bytes live here; photos are files in the object store (see the
[photos decision](decisions.md)).

## The corpus

| Table | Holds | Notes |
|---|---|---|
| `listings` | One row per canonical listing: address, neighborhood, geo, `listing_type` (`sale`/`rent`), `property_type`, `status`, and the lifecycle timestamps. | `status` ∈ `active`/`in_contract`/`sold`/`rented`/`delisted`. `material_changed_at` drives resurfacing. |
| `listing_current` | The typed hot snapshot per listing: price, beds, baths, sqft, maintenance, common charges, taxes, DOM, description. | Kept separate from `listings` so price/beds/sqft queries stay fast and typed, not JSONB scans. `dom` is the provider's count as of the last enrichment, not a live clock. |
| `listing_sources` | Provenance per provider listing: `provider`, `provider_id`, `url`, source status, the raw payload, `missing_runs`, `last_run_id` (the run that last saw it), and `enriched_at` (last detail-page fetch). | The raw JSON is authoritative for reconciliation. `missing_runs` gates delisting; `enriched_at` lets a resumed crawl skip fresh detail pages. |
| `listing_fields` | Selected canonical value + which source it came from, per field. | Explainability seam; matters once a second provider exists. |
| `price_events` | Append-only price history: a baseline row when a listing is first seen, then one row per canonical price change. | Not appended every crawl. |
| `listing_photos` | Per-photo metadata: `position`, `provider`, `source_url`, `cached_path`, `mime_type`, `width`, `height`, `refreshed_at` (last cache write; the TTL sweep reads it). | `cached_path` is the object-store key; NULL = not cached. `photos_cached` counts non-NULL. No bytes here. |
| `ingest_runs` | One row per crawl run: scope hash, `one_off` (observes without owning absence), timings, `complete`, `suspect`, `listings_seen`, created/updated counts, `photo_failures`, `errors`. | The durable operational record that `lst status` reads. Drives drift detection. |
| `crawl_targets` | What the crawler should crawl: `kind` (`standing`/`once`), `status`, `enabled`, `areas[]`, `listing_type`, filters, `last_run_id`, `last_error`. | `standing` = recurring scope; `once` = a queued `request_crawl`. The ingest worker drains due rows. |

**Deliberately not stored:** provider-side derived numbers such as StreetEasy's
`netEffectiveRent` and closed `soldPrice` are dropped at conversion. The corpus
keeps the asking price and its history; a second provider would not agree on
those derived fields, and the raw payload in `listing_sources` still has them.

**How a crawl flows through them:** the crawler reads due `crawl_targets` →
fetches from the provider → upserts `listing_sources` (raw) → maps canonical
fields into `listings` + `listing_current` (+ `listing_fields`) → appends a
`price_events` row on first sight or a price change → caches thumbnails and records
`listing_photos` → writes one `ingest_runs` row summarizing the run.

## The taste state

| Table | Holds | Notes |
|---|---|---|
| `profile` | The single hard-filter profile: max price, min beds/baths, max monthly carrying, `listing_type`, neighborhoods, property types. | Exactly one row (`id` is a `true` singleton). Constraints only — no taste. |
| `verdicts` | Immutable log of explicit feedback: `listing_id`, `verdict` (`love`/`maybe`/`dislike`), optional `note`, `created_at`. | Never updated; the latest verdict for a listing is the current opinion. The authoritative taste evidence. |
| `rubric_versions` | Append-only taste rubric: `content` (you-authored via the caller model) and `through_verdict_id`. | Rubric is *stale* when `max(verdicts.id) > through_verdict_id`; that's the `rubric_stale` flag. A cache of taste, not ground truth. |
| `shown` | What's already been presented: `listing_id`, `shown_at`, `material_changed_at_seen`. | `get_candidates` excludes these until the listing materially changes, then it's eligible again. |
| `lists` / `list_items` | Curated lists (`slug`, `name`, `emoji`, `is_default`) and their members (`list_id`, `listing_id`, optional `note`, `added_at`). | Exactly one default `favorites` list, created by migration; members cascade when a list or listing is deleted. |

## Key relationships & rules

- Everything hangs off `listings.id` (foreign keys cascade on delete). The one
  exception is `rubric_versions.through_verdict_id`, which pins a verdict: a
  listing with a verdict a rubric was written through cannot be deleted by
  hand without dropping that rubric version first. Nothing in the code deletes
  listings.
- **Identity follows the provider id.** A relist under a new provider id is a
  new `listings` row; verdicts, `shown`, and list memberships stay on the old
  one, so the relisted copy surfaces again as unseen.
- **A listing is never mass-delisted from one bad crawl** — `missing_runs` only
  advances on complete, non-suspect, non-empty runs, so parser drift can't wipe
  the corpus. Absence is judged per crawl scope: a listing a filtered scope
  (say `max_price`) stops seeing is treated as gone from that scope, so
  `delisted` also covers "moved outside the scope's filters" (re-priced above
  the cap). It resurfaces as `active` the moment any scope sees it again.
- **`reset_state`** wipes the taste tables (`profile`, `verdicts`,
  `rubric_versions`, `shown`, `list_items`, custom `lists`) and the
  `crawl_targets` queue, but keeps the corpus and the empty favorites list.
- **Photos:** `listing_photos.cached_path` is the object key
  (`{provider}/{shard}/{sha256}.jpg`); the public `/img/{key}` and
  `/img/l/{id}/{n}` routes serve those bytes. Content-addressed and write-once,
  so identical photos across listings share one object.

## See also

- [`migrations/`](../migrations/): the authoritative SQL
- [the decisions record](decisions.md): schema rationale and the photos-as-files decision
- [the v0 design overview](property-radar-v0-spec.md): the shape at a glance (historical)
- [the tool surface](../README.md#the-tool-surface): the MCP tools / REST endpoints that read and write this data
