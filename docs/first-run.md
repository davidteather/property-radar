# First run — confirm your instance works

A five-minute smoke test once your instance is deployed and connected over MCP
(see the [deploy guides](deploying/README.md)). It's also the fastest way to
see the whole point of the project.

Register the MCP server in your client (Claude, ChatGPT) as `property-radar`,
then run two conversations.

## Conversation A — build some taste memory

1. "What does Property Radar know about my preferences so far?" — on a fresh
   install it should report an empty state, with no invented taste.
2. Set your real hard filters (max price, min beds, carrying cost, neighborhoods).
3. If the corpus is empty, ask it to crawl an area you care about ("crawl 2-bed
   sale listings in Park Slope under $1.2M") and wait for the first crawl to
   land — see [Getting listings in](../README.md#getting-listings-in-the-crawler).
4. "Show me 5 candidates that match my profile — look at the photos and pitch
   each." The photos should render inline in the chat.
5. React honestly and specifically on ~10 listings (visual details matter:
   "love the light", "hate the street-facing bedroom") so verdicts get recorded.
6. "Mark those as shown, then write my taste rubric through the latest verdict."
7. `get_state` should now show `rubric_stale = false`.

## Conversation B — the actual test (a brand-new chat)

Open a completely fresh conversation with no memory of the above and ask:

> "Recommend 5 homes using whatever Property Radar knows about me."

**It passes if** two to three of the picks explicitly cite your persisted
feedback — including visual preferences — and the set is clearly better than a
cold-start guess. Bonus: ask "why not \<a listing you disliked in A\>?" and it
should answer from the stored verdict.

That cross-conversation recall — a brand-new chat that already knows your
taste — is the whole thesis. If it works, your instance is set up correctly.

## Quick edge checks

- A nonexistent listing id → a clean error, not a hallucinated listing.
- Ask for something the corpus doesn't have (rentals in a sale-only corpus) → it
  should say so rather than invent results.
- "Which listings had price drops?" → the price-drop flag surfaces.
