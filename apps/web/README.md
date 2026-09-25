# Signa web styling

- Build layouts and component styles with Tailwind CSS v4 utilities.
- Keep `app/globals.css` for imports, Signa design tokens, resets, and shared base styles.
- Keep shared layout concerns in `AppShell`; keep route-specific layout and typography with that route.
- Add shadcn components only when a feature needs them. Use the configured aliases and `cn()` helper from `lib/utils.ts` for conditional or conflicting classes.
- Use `motion-safe:` and `motion-reduce:` variants for motion so reduced-motion preferences are respected.

## Incident map

The map uses MapLibre GL JS and generalized public incident geometry from the Go API. Set `NEXT_PUBLIC_MAP_STYLE_URL` to a public style URL for a provider approved for the deployment. This URL is visible to browsers; do not include credentials. Review the provider’s terms, rate limits, and privacy practices before configuring it. With no valid style configured, incident details remain available in the text list and the map shows an unavailable message.

## Route relevance boundary (COD-209)

The current OpenAPI contract has no browser-facing route request, route geometry response, or route/incident relevance operation. COD-208's OSRM-compatible adapter and COD-210's route policy are internal and request-scoped. The browser must not call the routing provider directly, infer relevance from visual proximity/overlap, or calculate alert priority.

The route panel accepts a typed presentation state; it is not an API schema. The map has an optional validated route-line display prop, but the production page supplies no route geometry and shows that route checks are unavailable. Geometry and relevance fixtures are limited to automated tests. Route lines are visual context only; only a future approved backend classification may label a route relevant or not relevant.

Before production integration, the backend/API owner must approve a versioned browser-facing contract and privacy policy covering:

- how a user explicitly submits a request-scoped origin, destination, and optional waypoints without persisting route geometry or exposing the OSRM provider;
- whether and at what precision a route line may be returned to the requesting browser, and how request identity, consent, logging, retention, and rate limits protect it;
- a generated public response that distinguishes route relevance (`RELEVANT`, `NOT_RELEVANT`, or `UNKNOWN`) from incident-data-unavailable and routing/request failures, and identifies only incidents safe for public display;
- timeout, retry, freshness, and partial-result behavior, without treating absent data or `NOT_RELEVANT` as a safety guarantee.

No endpoint path, request/response schema, provider URL, route persistence, or browser integration is defined here. The follow-up is for the owner to approve that contract (and any necessary ADR/privacy decision), add it to OpenAPI and the backend, then wire the generated client to these presentation and map boundaries.
