# StreetEasy provider fixtures

**Synthetic** fixtures backing the `internal/streeteasy` parser tests. The wire
*shape* mirrors the provider's API/HTML responses so the extractor can be tested
offline and hermetically, but **all listing content is fabricated** —
descriptions, street addresses, unit/building slugs, photo keys, and
broker/agent names are invented. No real listing data or copyrighted prose is
committed here; only non-identifying structural facts (field shapes, enums,
counts) reflect the provider.

Also stripped before check-in (never committed): search ids, request headers,
cookies, `set-cookie`, any auth/search tokens, proxy credentials, operator IP,
Webshare material, map/browser API keys, and fingerprint GUIDs.

> Provider-specific request details (endpoints, query bodies, schema notes) are
> intentionally **not** documented in this repository. This is a personal,
> non-commercial tool; the fixtures exist only to test the parser against a
> fabricated copy of the response shape.

## Files

| file | what |
| --- | --- |
| `search_brownstone_brooklyn.json` | sales search response, one edge per building type |
| `search_upper_west_side_page1.json` | sales search, page 1, `hasNextPage: true` |
| `search_upper_west_side_page2.json` | sales search, page 2, `hasNextPage: false` |
| `detail_coop_1777453.json` | resolved detail `listing` object, CO_OP |
| `detail_condo_1644015.json` | resolved detail `listing` object, CONDO |
| `detail_townhouse_1832614.json` | resolved detail `listing` object, TOWNHOUSE |
| `detail_page_coop_1777453.html` | co-op detail page reduced to its RSC scripts — exercises the full extractor |
| `search_rentals_brownstone_brooklyn_page1.json` | rentals search, `ACTIVE`, one edge per building type, `hasNextPage: true` |
| `search_rentals_brownstone_brooklyn_page2.json` | rentals search, page 2, `hasNextPage: false` |
| `search_rentals_rented.json` | `RENTED` rentals — the only source of non-`ACTIVE` rental statuses |
| `detail_rental_4864627.json` | resolved detail `listing` object, RENTAL |
| `detail_rental_coop_5056345.json` | resolved detail `listing` object, CO_OP rental |
| `detail_rental_rented_9140.json` | resolved detail `listing` object, `status: RENTED` |
| `detail_page_rental_4864627.html` | rental detail page reduced the same way — proves the extractor is listing-type agnostic |

The area taxonomy (`areas.json`) lives in `internal/streeteasy/` (embedded at
build time), not here.
