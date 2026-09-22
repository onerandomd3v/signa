# Signa — System Architecture

**Document:** `architecture.md`  
**Project:** Signa  
**Status:** Architecture baseline for MVP  
**Audience:** Product, engineering, design, operations  
**Last updated:** 2026-09-22

---

## 1. Purpose

Signa is a web-based, real-time community intelligence and safety platform.

Its purpose is to turn fragmented local reports into continuously updated, confidence-aware incidents and distribute the most relevant information to the people who need it first.

The architecture is designed around one primary product requirement:

> **Reduce the time between a meaningful local event being reported and a relevant person receiving a trustworthy, appropriately qualified warning.**

This document defines the architecture we are building toward before implementation begins. It intentionally focuses on system boundaries, responsibilities, data flow, reliability, geospatial behavior, realtime delivery, AI responsibilities, privacy, and deployment principles.

It is **not** yet a detailed implementation plan or Linear backlog.

---

## 2. Product Context

A user may report a local event using text and, when available, media such as an image, audio recording, or video.

The system must then:

1. accept the report quickly;
2. understand what is being claimed;
3. establish where and when it is being claimed;
4. compare it with existing incidents;
5. identify duplicate or related reports;
6. assess report evidence;
7. measure independent corroboration;
8. update incident confidence and severity;
9. determine which users are affected;
10. assign user-specific priority such as P1, P2, or P3;
11. deliver a realtime alert;
12. continue updating the incident until it resolves or expires.

The platform must never equate "AI output" with "truth." AI helps interpret evidence. Deterministic product logic decides how the system behaves.

---

# 3. Working Product Name

## Signa

**Signa** is the current working name.

The name reflects two important product ideas:

- **Near** — proximity, route relevance, hyperlocal awareness.
- **Signal** — useful information extracted from noise.

The name is intentionally broader than "security," "emergency," or "crime" so the system can later support other high-value local intelligence use cases without changing its identity.

This is a **working codename**, not final trademark or domain clearance.

---

# 4. Architectural Principles

## 4.1 Fast acknowledgement before deep processing

A report should be accepted and persisted quickly.

AI extraction, clustering, media processing, corroboration, and notification fan-out should happen asynchronously wherever possible.

A slow AI provider must not prevent the original report from entering the system.

## 4.2 Durable before realtime

Realtime behavior is valuable only if events are not silently lost.

Critical transitions must be durable and recoverable.

Examples:

- a report was created;
- an incident changed confidence;
- an alert became eligible for delivery;
- a notification failed and requires retry.

The architecture favors recoverable event processing over fire-and-forget messaging.

## 4.3 Geography is a core data primitive

Location is not an optional metadata field.

The platform must be able to answer questions such as:

- Which incidents are near this user?
- Which reports fall inside the same event area?
- Does a user's route intersect an affected zone?
- Which users should receive a P1 alert?
- Which active incidents overlap geographically?

Geospatial operations therefore belong in the primary data architecture.

## 4.4 Evidence, confidence, severity, and priority are separate

The system must not collapse everything into one score.

Signa models four separate concepts:

### Report Evidence
How strong is this individual report?

### Incident Confidence
How much independent, fresh, consistent evidence supports the incident?

### Incident Severity
If the reported incident is real, how serious is it?

### User Priority
How urgently does this particular user need to know?

A high-severity event can have low confidence.  
A high-confidence event can be irrelevant to a particular user.

## 4.5 AI interprets; rules decide

AI may:

- extract event type;
- extract location/time references;
- transcribe audio;
- understand local language or Pidgin;
- summarize evidence;
- suggest likely duplicates;
- identify contradictions;
- inspect media;
- produce structured classifications.

AI should **not** directly:

- mark an incident as unquestionably true;
- decide final P1/P2/P3 delivery on its own;
- bypass configured thresholds;
- send public alerts without deterministic system rules;
- overwrite human or audited state without a recorded transition.

## 4.6 Privacy is part of safety

Exact reporter location may itself be sensitive.

The system should collect only what is necessary and should avoid retaining continuous user movement unless a feature explicitly requires it.

Public-facing incident information should expose an appropriate area or zone rather than a reporter's exact position.

## 4.7 Text-first, media-optional

Media must strengthen evidence without becoming a prerequisite for reporting.

A user on a weak connection must be able to submit text immediately.

Media can upload asynchronously and update the incident later.

## 4.8 Modular monolith before microservices

The MVP should not begin as many independent services.

The first backend should be a modular Go application with clear domain boundaries and separate worker processes.

Modules may later be extracted into services if real operational or scaling pressure justifies it.

---

# 5. High-Level Architecture

```text
                         ┌──────────────────────────────┐
                         │       RESPONSIVE WEB APP     │
                         │  Next.js + React + TypeScript│
                         └──────────────┬───────────────┘
                                        │
                           HTTPS / SSE / Web Push
                                        │
                                        ▼
                         ┌──────────────────────────────┐
                         │        GO API SERVICE        │
                         │ auth • reports • incidents   │
                         │ verification • alerts        │
                         └───────┬─────────┬────────────┘
                                 │         │
                    ┌────────────┘         └───────────────┐
                    ▼                                      ▼
        ┌────────────────────────┐             ┌──────────────────────┐
        │ PostgreSQL + PostGIS   │             │  Object Storage R2   │
        │ source of truth        │             │ image/audio/video    │
        └────────────┬───────────┘             └──────────────────────┘
                     │
                     │ transactional outbox
                     ▼
             ┌───────────────────┐
             │   Redis Streams   │
             │ durable event bus │
             └─────────┬─────────┘
                       │
       ┌───────────────┼─────────────────────┐
       ▼               ▼                     ▼
┌──────────────┐ ┌──────────────┐    ┌─────────────────┐
│ AI Workers   │ │Incident/Score│    │Delivery Workers │
│ extract      │ │Workers       │    │SSE / Push       │
│ transcribe   │ │cluster       │    │retries          │
│ classify     │ │confidence    │    │fan-out          │
└──────┬───────┘ └──────┬───────┘    └────────┬────────┘
       │                │                      │
       └────────────────┴──────────────────────┘
                        │
                        ▼
              PostgreSQL / Redis state
```

---

# 6. Technology Baseline

| Layer | Technology | Why it exists |
|---|---|---|
| Web client | Next.js + React + TypeScript | Responsive web application, map-heavy UI, forms, dashboards, PWA path |
| Core backend | Go | Concurrency, predictable network services, workers, SSE, fan-out, operational simplicity |
| API routing | Go standard library or thin router such as Chi | Keep HTTP layer small; final router can be locked during implementation |
| API contract | OpenAPI | Stable boundary between Go backend and TypeScript frontend |
| Primary database | PostgreSQL | Transactional source of truth |
| Geospatial database | PostGIS | Radius, intersection, proximity, zones, route-related queries |
| Event processing | Redis Streams | Durable low-latency queues/streams, consumer groups, retryable workers |
| Cache / ephemeral state | Redis | Rate limiting, short-lived state, fast lookup where appropriate |
| Foreground realtime | Server-Sent Events (SSE) | Mostly server-to-client realtime updates without unnecessary bidirectional protocol complexity |
| Background browser delivery | Web Push + Service Worker | Notify users when the web app is not in the foreground |
| Maps | MapLibre GL JS | Flexible interactive map rendering |
| Routing | External routing provider initially | Route geometry and alternative routes without operating routing infrastructure |
| Media storage | Cloudflare R2 or S3-compatible object storage | Large binary assets outside PostgreSQL |
| AI extraction | OpenAI structured multimodal API initially | Structured event extraction and image understanding |
| Audio transcription | OpenAI speech-to-text initially | Convert voice reports into text for downstream processing |
| Observability | OpenTelemetry + error monitoring | Trace report-to-alert latency and failures across services |
| Deployment | Containers + managed Postgres/Redis initially | Controlled runtime without premature Kubernetes |
| Infrastructure strategy | Managed where practical | Small team should focus on product reliability rather than operating commodity infrastructure |

---

# 7. Frontend Architecture

## 7.1 Platform

Signa is a **web-first desktop application with mobile responsiveness designed from the start**.

Desktop is the main development surface, but no core workflow may depend on desktop-only interaction.

The same product must remain usable on:

- modern desktop browsers;
- tablets;
- mobile browsers;
- low-bandwidth mobile connections.

## 7.2 Primary frontend responsibilities

The web app is responsible for:

- authentication and session UX;
- submitting reports;
- direct media upload coordination;
- requesting browser geolocation with consent;
- showing incident state;
- map visualization;
- verification requests;
- verifier/operations dashboards;
- realtime SSE consumption;
- Web Push subscription;
- route-related incident display;
- low-bandwidth/error states.

## 7.3 Frontend must not own critical truth

The browser must not be authoritative for:

- confidence state;
- severity;
- P1/P2/P3 assignment;
- reporter reputation;
- delivery eligibility;
- incident resolution;
- evidence independence.

The browser renders server-authoritative state.

---

# 8. Go Backend Architecture

The Go backend begins as a **modular monolith**.

Recommended logical modules:

```text
/internal
  /auth
  /users
  /reports
  /media
  /incidents
  /evidence
  /corroboration
  /confidence
  /severity
  /priority
  /geospatial
  /verification
  /notifications
  /reputation
  /audit
  /outbox
```

Recommended executable boundaries:

```text
/apps/web          -> Next.js application

/cmd/api           -> Go HTTP API + SSE endpoints
/cmd/worker        -> Go background worker process
```

The API and workers may share internal domain packages while remaining separate deployable processes.

---

# 9. Core Domain Model

## 9.1 Report

A report is one user's claim/evidence submission.

Example fields:

```text
Report
- id
- reporter_id
- incident_id?
- raw_text
- normalized_text
- source_type
- eyewitness_claim
- claimed_location
- observed_at
- submitted_at
- device_location?
- location_accuracy?
- language
- evidence_state
- created_at
```

A report may exist before it is attached to an incident.

## 9.2 Media Evidence

```text
MediaEvidence
- id
- report_id
- object_key
- media_type
- upload_state
- capture_source
- captured_at?
- uploaded_at
- analysis_state
- metadata_summary
```

Media is stored in object storage.

PostgreSQL stores metadata and references, not the binary object.

## 9.3 Incident

An incident represents a live event inferred from one or more reports.

```text
Incident
- id
- event_type
- status
- confidence_state
- severity
- center_point
- affected_geometry?
- started_at?
- last_signal_at
- expires_at?
- resolved_at?
- created_at
- updated_at
```

The incident is the object users follow.

Users should not experience ten separate posts when the evidence refers to one evolving event.

## 9.4 Incident Report Link

Reports linked to an incident need metadata describing the relationship.

```text
IncidentReport
- incident_id
- report_id
- similarity
- independence_weight
- contradiction_state
- attached_at
```

This allows multiple reports to support, contradict, or merely repeat an existing incident.

## 9.5 Alert

```text
Alert
- id
- incident_id
- alert_type
- confidence_snapshot
- severity_snapshot
- message
- created_at
- supersedes_alert_id?
```

Alerts represent distributable snapshots of an evolving incident.

## 9.6 Delivery

```text
Delivery
- id
- alert_id
- user_id
- priority
- channel
- state
- attempts
- last_attempt_at
- delivered_at?
```

Delivery state must be auditable and retryable.

---

# 10. Report Ingestion Flow

```text
User submits text
      │
      ▼
Go API validates request
      │
      ▼
Persist report in PostgreSQL
      │
      ├── create outbox event in same transaction
      │
      ▼
Return acknowledgement quickly
      │
      ▼
Outbox publisher sends report.created
to Redis Streams
      │
      ▼
Workers begin asynchronous processing
```

The user should not wait for:

- transcription;
- AI extraction;
- media processing;
- duplicate search;
- confidence calculation;
- recipient calculation.

The report is accepted first.

---

# 11. Transactional Outbox

Signa should avoid this failure mode:

```text
1. report committed to PostgreSQL
2. application crashes
3. report.created was never published
```

Instead:

```text
BEGIN TRANSACTION

INSERT report
INSERT outbox_event(report.created)

COMMIT
```

A publisher reads pending outbox records and writes them to Redis Streams.

After successful publication, the outbox record is marked as published.

This gives the system a recoverable bridge between transactional state and asynchronous event processing.

---

# 12. Event Backbone

Redis Streams is the MVP event backbone.

Possible streams/events include:

```text
report.created
report.media_attached
report.ai_processed
report.location_resolved
incident.created
incident.report_attached
incident.confidence_changed
incident.severity_changed
incident.status_changed
incident.resolved
alert.created
delivery.requested
delivery.succeeded
delivery.failed
```

Consumers must use:

- consumer groups;
- acknowledgements;
- idempotent handlers;
- retry policies;
- dead-letter or quarantine behavior for repeatedly failing work.

Redis is not the source of truth for incidents.

PostgreSQL remains authoritative.

---

# 13. AI Processing Pipeline

AI processing is asynchronous.

## 13.1 Text extraction

AI may convert:

> "Abeg avoid market junction, I hear shots there like 5 mins ago"

into a structured payload such as:

```json
{
  "event_type": "possible_gunfire",
  "location_text": "Market Junction",
  "time_reference": "approximately_5_minutes_ago",
  "source_claim": "auditory_observation",
  "language": "Nigerian Pidgin",
  "severity_candidate": "critical"
}
```

The structured response must be schema validated.

## 13.2 Audio

Audio follows:

```text
upload
  ↓
object storage
  ↓
speech-to-text
  ↓
structured event extraction
  ↓
attach evidence result
```

Audio upload failure must not invalidate the original text report.

## 13.3 Image/video

Media analysis may contribute signals such as:

- whether visual content is consistent with the event category;
- whether obvious duplicate media already exists;
- whether capture metadata is available;
- whether the scene plausibly matches other submitted evidence.

Media must not receive an automatic "high truth score" merely because it is a video.

---

# 14. Evidence and Confidence Model

Signa should avoid exposing a single mysterious numeric "truth score."

Internally, normalized numerical features may exist, but product state should remain explainable.

## 14.1 Report Evidence

Inputs may include:

- first-hand vs hearsay claim;
- reporter proximity;
- freshness;
- specificity;
- original media;
- capture context;
- reporter reliability history;
- consistency with known geography;
- detected duplication;
- internal contradiction.

## 14.2 Independent corroboration

Five reports are not necessarily five independent sources.

The system should reduce weight when evidence appears to share the same origin.

Possible indicators:

- identical or near-identical text;
- same media hash;
- forwarded content;
- common source URL;
- highly synchronized submissions;
- explicit "someone sent me this" language.

Independent eyewitness reports from different nearby users should contribute more than repeated forwards.

## 14.3 Incident Confidence States

MVP states:

```text
UNVERIFIED
    ↓
EMERGING
    ↓
CORROBORATED
    ↓
HIGH_CONFIDENCE
```

An incident may also become:

```text
DISPUTED
RESOLVING
RESOLVED
EXPIRED
```

State transitions must be recorded.

---

# 15. Severity Model

Severity answers:

> If the reported incident is real, how serious is it?

Example categories:

```text
LOW
MODERATE
HIGH
CRITICAL
```

Severity is separate from confidence.

Example:

```text
Possible gunfire:
severity = CRITICAL
confidence = EMERGING
```

A critical but not-yet-confirmed event may still justify a carefully worded local precautionary alert.

---

# 16. Priority Model — P1 / P2 / P3

Priority is calculated per user, not globally.

## P1 — Immediate relevance

Typical conditions:

- user is inside or very near the affected zone;
- user's current/planned route intersects the incident;
- severity is sufficiently high;
- information is fresh enough to affect an immediate decision.

## P2 — Nearby awareness

Typical conditions:

- user is in the surrounding area;
- event may affect movement or local behavior soon;
- user is not presently inside the immediate affected zone.

## P3 — Wider community awareness

Typical conditions:

- event is relevant to a broader area;
- immediate user action is unlikely;
- information is useful but not urgent.

Priority may consider:

```text
priority =
  proximity
  + route relevance
  + severity
  + confidence
  + freshness
```

This is conceptually descriptive. Final implementation should use explicit tested rules rather than one opaque formula.

---

# 17. Geospatial Architecture

PostGIS is the system of record for spatial relationships.

Likely geometry types:

```text
Report.device_location       -> POINT
Report.claimed_location      -> POINT / approximate area
Incident.center_point        -> POINT
Incident.affected_geometry   -> POLYGON / radius-derived geometry
Route.geometry               -> LINESTRING (when stored temporarily)
```

Core spatial operations include:

- users within radius;
- reports within incident radius;
- nearest active incidents;
- route/incident intersection;
- affected-zone membership;
- clustering candidate search.

Spatial indexes are required for production queries.

---

# 18. Location Privacy Model

Exact location should be treated as sensitive.

Default approach:

```text
browser exact location
        ↓
secure API
        ↓
relevance / proximity processing
        ↓
store only required precision
        ↓
public UI receives approximate area
```

Potential controls:

- location consent is explicit;
- exact report location is restricted internally;
- public incident display uses generalized zones;
- no public reporter coordinates;
- avoid persistent continuous tracking by default;
- route data should be short-lived unless a clear feature requires retention.

---

# 19. Incident Lifecycle

An incident is a living object.

Example:

```text
18:37 first report
      ↓
EMERGING

18:39 second independent report
      ↓
CORROBORATED

18:42 supporting media + verifier signal
      ↓
HIGH_CONFIDENCE

18:55 recent reports indicate reduced activity
      ↓
RESOLVING

19:20 no fresh supporting signals
      ↓
RESOLVED / EXPIRED
```

Freshness is fundamental.

Old information must not remain active indefinitely.

---

# 20. Freshness and Decay

Each incident category should eventually have a freshness policy.

Examples:

- possible gunfire may become stale quickly;
- a road closure may remain relevant longer;
- flooding may remain relevant for hours;
- an accident may persist until a road-clear signal arrives.

The MVP may begin with simple configurable TTL/decay rules.

Decay should affect:

- confidence;
- notification eligibility;
- map prominence;
- verification prompts;
- expiration behavior.

---

# 21. Realtime Delivery Architecture

Signa uses two browser delivery modes.

## 21.1 SSE — app open

Server-Sent Events are used for live server-to-client updates while the application is open.

Examples:

- incident confidence changes;
- incident resolved;
- verifier queue updated;
- new P1/P2 incident appears;
- alert wording updated.

## 21.2 Web Push — app background/closed

Web Push is used for browser notifications when the web application is not active.

Web Push is not assumed to be the only future safety channel.

The notification architecture should expose a channel abstraction so later adapters can support:

```text
WEB_PUSH
SMS
WHATSAPP
VOICE
EMAIL (non-urgent operational use)
```

---

# 22. Notification Delivery

Delivery workers consume `delivery.requested`.

Responsibilities:

1. load delivery record;
2. confirm it has not already succeeded;
3. send through the selected channel;
4. record provider response;
5. acknowledge success;
6. retry transient failures;
7. quarantine permanent/repeated failures.

All critical delivery handlers must be idempotent.

A retry must not result in duplicate user notifications when the provider has already accepted the message.

---

# 23. Media Architecture

Media is uploaded directly to object storage using temporary signed upload credentials/URLs where practical.

Recommended flow:

```text
1. create text report
2. backend returns report ID + upload authorization
3. browser uploads media directly to R2
4. browser/backend confirms completed object
5. media_attached event published
6. asynchronous analysis begins
```

Advantages:

- API servers do not proxy large video uploads;
- text report is not blocked by slow media;
- uploads can be retried independently;
- object storage handles large binary data efficiently.

---

# 24. API Contract

The Go backend and TypeScript frontend communicate through a documented API contract.

Use OpenAPI to define:

- endpoints;
- request schemas;
- response schemas;
- enums;
- error shapes;
- authentication requirements.

Generate TypeScript client types from the contract where practical.

This removes the need to share implementation-language types directly between Go and TypeScript.

---

# 25. Authentication and Roles

Initial roles may include:

```text
COMMUNITY_USER
TRUSTED_VERIFIER
OPERATOR
ADMIN
```

Authorization must be enforced on the server.

Examples:

- normal users can report and view eligible incidents;
- trusted verifiers receive verification requests;
- operators can review incident/evidence timelines;
- admins manage operational configuration.

Role status should not expose unnecessary personal information publicly.

---

# 26. Auditability

Signa must be able to explain important system transitions.

Record events such as:

```text
report submitted
report attached to incident
incident confidence changed
severity changed
human verifier confirmed
human verifier disputed
alert created
priority assigned
notification requested
notification delivered
incident resolved
```

For sensitive automated decisions, preserve:

- the rule version;
- relevant evidence snapshot;
- AI structured output where appropriate;
- human action if any;
- timestamp;
- actor/service.

This enables debugging and later policy review.

---

# 27. Reliability Requirements

The MVP should be designed so that:

- reports are never intentionally dropped due to downstream AI failure;
- failed workers can retry;
- duplicate event delivery does not corrupt state;
- database transitions are transactional;
- alert delivery state is recorded;
- stale incidents expire;
- service failures are visible through monitoring.

Signa should degrade gracefully.

Example:

If AI is temporarily unavailable:

```text
report still persists
incident may remain pending
operator/verifier can still review it
AI processing retries later
```

---

# 28. Observability

The most important metric is not generic API latency.

The key product latency is:

```text
event report submitted
        ↓
report persisted
        ↓
report understood
        ↓
incident updated
        ↓
recipient identified
        ↓
alert delivered
```

OpenTelemetry should trace this path.

Important metrics:

- report API acknowledgement latency;
- outbox publication latency;
- AI processing latency;
- incident clustering latency;
- confidence-update latency;
- P1 recipient calculation latency;
- notification queue time;
- delivery success/failure;
- end-to-end report-to-alert latency;
- duplicate notification rate;
- stream backlog;
- failed worker count.

---

# 29. Deployment Strategy

The MVP should avoid Kubernetes.

Initial deployment model:

```text
Next.js web container
Go API container
Go worker container(s)
Managed PostgreSQL + PostGIS
Managed Redis
R2 object storage
```

Scale workers horizontally when needed.

This keeps infrastructure understandable while preserving a clear path to scale.

---

# 30. Security Baseline

Minimum requirements:

- HTTPS everywhere;
- secure session/authentication handling;
- encrypted provider credentials/secrets;
- private object storage by default;
- signed temporary media access;
- strict role authorization;
- request rate limiting;
- report abuse controls;
- idempotency keys for critical mutations;
- audit logs;
- input/schema validation;
- database least-privilege access;
- no exact reporter coordinates in public APIs.

---

# 31. Abuse and Manipulation Resistance

The platform must assume attackers may try to create false incidents.

Initial controls should consider:

- account age/reputation;
- device/session uniqueness;
- submission rate;
- report similarity;
- media duplication;
- location plausibility;
- contradictory local reports;
- coordinated submission patterns;
- historically unreliable reporters.

High report count alone must never equal high confidence.

---

# 32. What We Are Not Building Yet

The MVP should not begin with:

- Kafka;
- Kubernetes;
- many microservices;
- custom ML model training;
- face recognition;
- predictive policing;
- autonomous "truth detection";
- satellite intelligence;
- national-scale government integrations;
- always-on background location tracking;
- complex graph databases;
- native iOS/Android applications.

These may be reconsidered only when product evidence creates a real requirement.

---

# 33. Suggested Repository Shape

```text
signa/
│
├── apps/
│   └── web/                 # Next.js / TypeScript
│
├── cmd/
│   ├── api/                 # Go API executable
│   └── worker/              # Go background worker executable
│
├── internal/
│   ├── auth/
│   ├── users/
│   ├── reports/
│   ├── incidents/
│   ├── evidence/
│   ├── confidence/
│   ├── severity/
│   ├── priority/
│   ├── geospatial/
│   ├── verification/
│   ├── notifications/
│   ├── reputation/
│   ├── audit/
│   └── outbox/
│
├── migrations/              # PostgreSQL/PostGIS migrations
├── contracts/
│   └── openapi.yaml
│
├── deploy/
│   └── container/config
│
├── docs/
│   ├── architecture.md
│   └── adr/
│
├── go.mod
└── README.md
```

The exact monorepo tooling for the TypeScript application can be selected when repository setup begins.

---

# 34. Architecture Decision Records

Before implementation, create short ADRs for the decisions that matter most.

Suggested first ADRs:

```text
ADR-001: Go as the core backend language
ADR-002: PostgreSQL + PostGIS as the system of record
ADR-003: Redis Streams as the MVP event backbone
ADR-004: Transactional outbox for durable event publication
ADR-005: SSE for foreground realtime updates
ADR-006: Web Push for initial background browser alerts
ADR-007: Modular monolith before microservices
ADR-008: OpenAPI contract between Go and TypeScript
ADR-009: AI interpretation separated from deterministic decisions
ADR-010: Location minimization and approximate public geography
```

---

# 35. First Vertical Slice

The first engineering milestone should prove one complete path rather than many disconnected components.

## Vertical slice

```text
1. User opens responsive web app
2. User submits text report + browser location
3. Go API validates and persists report
4. Outbox publishes report.created
5. Worker performs structured AI extraction
6. System creates or attaches to incident
7. Basic confidence rule runs
8. PostGIS identifies nearby test user
9. P1/P2/P3 rule runs
10. Alert is created
11. Test user receives SSE update
12. Incident can later be marked resolved
```

Media, routing, advanced reputation, and multi-channel notification can be layered on after this core loop works reliably.

This slice directly tests the central Signa hypothesis:

> Can a local report become a structured incident and reach the right nearby person quickly and reliably?

---

# 36. Decisions Still to Lock Before Coding

The architecture direction is established, but several lower-level decisions should be made before implementation tickets are created:

1. Go HTTP router: standard `net/http` vs Chi.
2. PostgreSQL hosting provider.
3. Redis hosting provider.
4. Authentication provider vs in-house session implementation.
5. Initial routing provider.
6. R2 vs another S3-compatible storage provider.
7. Exact MVP incident state machine.
8. Initial confidence transition rules.
9. Exact P1/P2/P3 thresholds.
10. Location-retention precision and duration.
11. Media size/type limits.
12. Initial alert delivery SLO.
13. Pilot scale assumptions.
14. Error monitoring provider.
15. CI/CD provider and environment strategy.

These choices should be made through explicit ADRs rather than silently embedded in code.

---

# 37. Definition of Architectural Success

The MVP architecture is successful if it can reliably demonstrate:

- a report can be submitted under weak/mobile conditions;
- the report is persisted before expensive processing begins;
- AI transforms it into structured evidence;
- related reports can update one incident;
- incident state is durable and auditable;
- PostGIS can determine local relevance;
- P1/P2/P3 can be calculated independently from confidence;
- a relevant user receives an update in near real time;
- failures are retryable and observable;
- media can arrive later without blocking text;
- exact reporter location is not unnecessarily exposed;
- the same foundation can grow beyond one pilot community without rewriting the entire system.

---

# 38. Architecture Summary

Signa uses:

```text
Next.js + TypeScript
        ↓
Go API
        ↓
PostgreSQL + PostGIS
        ↓
Transactional Outbox
        ↓
Redis Streams
        ↓
Go Workers
        ↓
AI / Confidence / Priority / Delivery
        ↓
SSE + Web Push
```

Supporting systems:

```text
MapLibre         -> map rendering
Routing provider -> route geometry
R2/S3 storage    -> media
OpenAPI          -> Go/TypeScript contract
OpenTelemetry    -> end-to-end observability
```

The central architectural rule is:

> **AI interprets evidence. Durable system state and deterministic rules decide what Signa does with that evidence.**

The central product rule is:

> **The strongest signal is not the loudest report. It is independent, fresh, location-consistent evidence delivered to the people for whom it matters now.**
