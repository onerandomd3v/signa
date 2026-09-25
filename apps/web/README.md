# Signa web styling

- Build layouts and component styles with Tailwind CSS v4 utilities.
- Keep `app/globals.css` for imports, Signa design tokens, resets, and shared base styles.
- Keep shared layout concerns in `AppShell`; keep route-specific layout and typography with that route.
- Add shadcn components only when a feature needs them. Use the configured aliases and `cn()` helper from `lib/utils.ts` for conditional or conflicting classes.
- Use `motion-safe:` and `motion-reduce:` variants for motion so reduced-motion preferences are respected.

## Incident map

The map uses MapLibre GL JS and generalized public incident geometry from the Go API. Set `NEXT_PUBLIC_MAP_STYLE_URL` to a public style URL for a provider approved for the deployment. This URL is visible to browsers; do not include credentials. Review the provider’s terms, rate limits, and privacy practices before configuring it. With no valid style configured, incident details remain available in the text list and the map shows an unavailable message.

Route relevance uses the authenticated `POST /v1/route-relevance` contract. The browser supplies only one request-scoped origin, destination, and optional waypoints; it renders the server-authoritative `RELEVANT`, `NOT_RELEVANT`, or `UNKNOWN` classification and may display the returned validated LineString for that request. `NOT_RELEVANT` never means safe or clear. The backend does not persist or publish route inputs/geometry, and the COD-210 priority engine still does not return geometry as part of priority reasons or events.
