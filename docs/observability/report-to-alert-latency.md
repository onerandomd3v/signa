# Report-to-alert latency diagnostics

`cmd/report-trace` is a bounded, operator-only PostgreSQL diagnostic for reconstructing the stages from report persistence through alert delivery. It does not add a public API, calculate a health score, or define latency objectives.

```sh
go run ./cmd/report-trace -report-id 00000000-0000-4000-8000-000000000001
go run ./cmd/report-trace -limit 20
```

Exactly one mode is required. `-report-id` accepts a non-zero UUID. `-limit` accepts 1–100 and returns the newest persisted reports. The command reads `SIGNA_DATABASE_URL` through the repository configuration (defaulting to the local development database), requires PostgreSQL to be ready, and emits indented JSON to stdout. Errors go to stderr and return a non-zero exit code.

## Stage definitions and correlation

The read model follows this deterministic path:

1. report persistence (`reports.created_at`); request acknowledgement/persistence duration remains unavailable because the current schema has no request-start timestamp;
2. the first `report.created` outbox event for the report and its publication timestamp;
3. AI processing from the durable processing-state row, or a persisted extraction when available;
4. incident attachment from `incident_reports.attached_at`;
5. priority evaluation, unavailable until the system persists a trustworthy evaluation timestamp;
6. the earliest alert for that incident at or after report attachment;
7. the earliest delivery for that alert, followed by its latest attempt state and first attempt start;
8. end-to-end report-to-alert and report-to-delivery elapsed time, when the corresponding timestamps exist.

Recent mode limits reports before the correlated lateral lookups. The report outbox and alert-delivery ordering indexes support those lookups. The read model reports stage durations only when both real boundary timestamps exist; it clamps out-of-order timestamps to zero. It never derives priority timing from alert `as_of` or treats a missing future stage as a zero-duration success.

`current_bottleneck` is the earliest pending/backlogged or failed stage in path order. State values distinguish completed, pending/backlogged, failed/retryable, failed/terminal, and unavailable/not-yet-measurable. Retry/error details are reduced to fixed safe categories; raw diagnostic errors are not returned.

## Privacy and interpretation

Output contains report/incident/alert/delivery correlation IDs, stage timing/state, and safe failure categories only. It excludes report text, reporter/user identities, exact coordinates, alert message text, push endpoints, delivery payloads, provider responses, and raw errors. Treat the operator's terminal output as operational data and do not paste it into public issues or logs.

Durations are observations, not guarantees. No numeric MVP latency thresholds or safety/health score are defined here. “Unavailable” means the system lacks a durable timestamp or an observable downstream event; it does not mean zero latency or that an area is safe.

## OpenTelemetry boundary

The Go code uses the OpenTelemetry API at report persistence, outbox publication, AI processing, incident processing, priority evaluation, alert creation, and delivery-attempt boundaries. It records stable stage names, generic success/error status, and elapsed duration; it attaches no report, user, incident, message, location, or provider identifiers and does not record error text. No SDK, exporter, collector endpoint, sampling policy, or production telemetry destination is configured by this change. Without a configured provider, the API uses its no-op implementation. A future approved runtime setup can install SDK providers/exporters without changing these instrumentation call sites.

The command's durable PostgreSQL correlation is the operator's end-to-end diagnostic path today; separately processed spans are not represented as one distributed parent-child trace unless a future event-propagation design explicitly adds privacy-reviewed context propagation.
