# Deterministic priority policy

The policy version is `signa.priority.v1`. Priority is calculated per user and
does not change incident status, confidence, or severity.

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
