# Deterministic priority policy

The location-only policy version is `signa.priority.v1`. Route-aware
evaluation uses `signa.priority.v2`; v1 remains available and unchanged.
Priority is calculated per user and does not change incident status,
confidence, or severity.

## Rule table

Every evaluation first requires an active (`OPEN` or `RESOLVING`) incident,
fresh incident activity, and a fresh user-location snapshot. `RESOLVED` and
`EXPIRED` incidents return `NONE`.

| Result | Required conditions |
| --- | --- |
| `P1` | Known accuracy; `distance + accuracy <= P1 radius`; severity `HIGH` or `CRITICAL`; confidence `EMERGING`, `CORROBORATED`, or `HIGH_CONFIDENCE`. |
| `P2` | Known accuracy; `distance + accuracy <= P2 radius`; severity `MODERATE`, `HIGH`, or `CRITICAL`; confidence `EMERGING`, `CORROBORATED`, or `HIGH_CONFIDENCE`; P1 did not match. |
| `P3` | `max(0, distance - accuracy) <= P2 radius` (raw distance is used when accuracy is unknown), with a known severity and confidence; P1/P2 did not match. |
| `NONE` | Terminal, stale/future, invalid/unknown incident values, or outside the P2 radius. |

Unknown accuracy cannot produce P1 or P2. It may produce P3 and always adds
`location_uncertain`. `DISPUTED` confidence may produce only P3; it never
silently behaves like corroborated evidence. A `CRITICAL` + `EMERGING`
incident can produce P1 when its spatial and freshness conditions match.

The policy consumes no coordinates. The evaluator loads the incident center
privately, queries COD-203 with explicit `AsOf` and `MaxAge`, and passes only
distance, accuracy, and observation time to the pure policy.

## Configuration

The worker requires these values without product-policy defaults:

- `SIGNA_PRIORITY_P1_RADIUS_METERS`
- `SIGNA_PRIORITY_P2_RADIUS_METERS`
- `SIGNA_PRIORITY_LOCATION_MAX_AGE`
- `SIGNA_PRIORITY_INCIDENT_MAX_AGE`

Both radii and both durations must be positive, and P1 radius must be smaller
than P2 radius.

## Route-aware v2 rule table

The spatial layer evaluates the request-scoped route against private incident
`affected_geometry` and passes only one categorical value to the pure policy:
`UNKNOWN`, `NOT_RELEVANT`, or `RELEVANT`. Center points alone produce
`UNKNOWN`; v2 does not invent a route corridor or reuse proximity radii.

| Route result | v2 behavior |
| --- | --- |
| `UNKNOWN` | Preserve the v1 level and add `route_relevance_unknown`. |
| `NOT_RELEVANT` | Preserve the v1 level and add `route_not_relevant`. |
| `RELEVANT` + existing P1/P2 | Preserve the existing level and add `route_relevant`. |
| `RELEVANT` + active/fresh actionable evidence | Promote a v1 `P3`/`NONE` result to `P2`; add `route_relevant` and `route_promoted_to_p2`. |
| `RELEVANT` + weaker or disputed evidence | At most `P3`; add `route_relevant` and, when promoted from `NONE`, `route_relevance_p3`. |
| terminal or stale incident | Always `NONE`, regardless of route result. |

Route geometry is validated, used only for the PostGIS intersection query, and
is never persisted, returned, or included in reasons/logs.
