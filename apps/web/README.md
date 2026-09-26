# Signa web styling

- Build layouts and component styles with Tailwind CSS v4 utilities.
- Keep `app/globals.css` for imports, Signa design tokens, resets, and shared base styles.
- Keep shared layout concerns in `AppShell`; keep route-specific layout and typography with that route.
- Add shadcn components only when a feature needs them. Use the configured aliases and `cn()` helper from `lib/utils.ts` for conditional or conflicting classes.
- Use `motion-safe:` and `motion-reduce:` variants for motion so reduced-motion preferences are respected.

## Incident map

The map uses MapLibre GL JS and generalized public incident geometry from the Go API. Set `NEXT_PUBLIC_MAP_STYLE_URL` to a public style URL for a provider approved for the deployment. This URL is visible to browsers; do not include credentials. Review the provider’s terms, rate limits, and privacy practices before configuring it. With no valid style configured, incident details remain available in the text list and the map shows an unavailable message.

## Route relevance (COD-209)

Route checks use the authenticated, generated `POST /v1/route-relevance` client. Users enter origin, destination, and optional ordered waypoints explicitly; the browser sends one request and does not track or persist route inputs. Requests use the configured Go API and the HttpOnly `signa_session` cookie.

The backend owns `RELEVANT`, `NOT_RELEVANT`, and `UNKNOWN`. The returned LineString is display-only and request-scoped; it is validated before drawing. Only generalized public incident projections are shown. `NOT_RELEVANT`, missing data, and failed checks do not mean a route is safe. The browser never calls the routing provider or computes relevance.

401/403, 429, 500/503, validation, and network failures are presented separately using the API contract. A 503 may indicate routing or public-incident dependency failure; the interface does not guess which unless the backend error code distinguishes it. Requests are abortable on edits, resubmission, and unmount. Route inputs and geometry are not persisted by the backend, per the approved contract.
