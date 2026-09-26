# ADR-0012: Opaque database-backed browser sessions

- Status: Accepted
- Date: 2026-09-25

## Context

Signa is a browser-first Next.js application with a separately deployed Go API. The API will expose authenticated long-lived SSE, and downstream handlers need a server-resolved principal rather than a user identity asserted by the browser. The MVP must avoid credentials in URLs, keep secrets out of logs, support expiry and revocation, remain compatible with OpenAPI-generated clients, and keep operational burden low.

## Decision

Signa will use one authentication/session mechanism: opaque, cryptographically random session secrets stored in an `HttpOnly` browser cookie named `signa_session`. The API stores only a SHA-256 hash of each secret in PostgreSQL, together with the stable `user_id`, expiry, revocation timestamp, and creation time. The raw secret is returned only when a trusted authentication flow creates a session and is never persisted or logged.

Protected HTTP routes use `internal/auth` middleware. The middleware reads the cookie, rejects missing or malformed values, hashes the presented secret, looks up an active unexpired session, and injects `auth.Principal{UserID}` into request context. Roles and authorization scopes are resolved by server-side services after the principal is established; clients cannot assert user IDs, incident IDs, alert IDs, roles, or scopes through headers or query parameters. Realtime owns delivery, not identity.

Cookies use `HttpOnly` and an explicit `Path=/`. Local development uses `Secure=false; SameSite=Lax`. Production with the Vercel frontend and separately deployed HTTPS API must use `Secure=true; SameSite=None` and an explicit allowed-origin CORS configuration with credentials enabled. SSE clients send the cookie through the browser's credentialed HTTP connection; no token is placed in an SSE URL.

Logout/revocation sets `revoked_at` for the session and clears the browser cookie. Expiry is enforced by the database lookup, and malformed, inactive, revoked, or expired sessions all produce the same unauthenticated response without disclosing credential details.

## Alternatives considered

Bearer tokens in the `Authorization` header were rejected for the MVP. They require additional browser token storage and refresh handling, are awkward for native `EventSource` unless a fetch-based SSE client is introduced, and increase the risk of token exposure to browser JavaScript. Cookies are native to browser requests and SSE while allowing the API to keep the reusable secret out of URLs and logs.

Client-asserted trusted headers and query parameters were rejected because they do not establish authenticity and could let a browser claim another user's identity or scope.

## Consequences

- PostgreSQL remains the durable source of session truth; Redis is not used for authentication identity.
- OpenAPI documents cookie authentication with `components.securitySchemes.CookieAuth`.
- Cross-origin credentialed requests require explicit origins and CORS credentials; wildcard origins are invalid.
- A future identity provider or login flow must create/revoke these server-side sessions rather than introduce a second request credential mechanism.
- User-role and incident/alert authorization remain separate server-side resolver boundaries for later issues.
