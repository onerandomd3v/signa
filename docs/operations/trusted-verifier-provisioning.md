# Trusted verifier pilot provisioning

COD-219 has no grant/revoke API, admin UI, or bootstrap flow. An authorized operator provisions and revokes pilot grants through controlled database/deployment access. Use the canonical UUID from the authenticated Signa principal; do not derive an ID from email, a device, or a client claim. Never store real user IDs in source control or deployment logs.

Apply the current Goose migrations before provisioning. In a restricted SQL session against the intended environment, bind `:verifier_user_id` to the verified principal UUID. Record an operational grant with a NULL actor; do not fabricate an operator identity:

```sql
BEGIN;
INSERT INTO user_capability_grants
    (user_id, capability, granted_by, grant_provenance)
VALUES
    (:'verifier_user_id'::uuid, 'trusted_verifier', NULL, 'operational_system');
COMMIT;
```

The partial unique index prevents a second active grant for the same user and capability. If the insert conflicts, inspect the existing active row rather than deleting or replacing it. Verify the active state with:

```sql
SELECT id, user_id, capability, granted_at, grant_provenance
FROM user_capability_grants
WHERE user_id = :'verifier_user_id'::uuid
  AND capability = 'trusted_verifier'
  AND revoked_at IS NULL;
```

Revoke by updating the active row, preserving its audit history. An operational revocation uses a NULL actor and explicit provenance. A zero-row result means no active grant was revoked; investigate before treating the operation as complete.

```sql
BEGIN;
UPDATE user_capability_grants
SET revoked_at = clock_timestamp(),
    revoked_by = NULL,
    revocation_provenance = 'operational_system'
WHERE user_id = :'verifier_user_id'::uuid
  AND capability = 'trusted_verifier'
  AND revoked_at IS NULL
RETURNING id, revoked_at, revocation_provenance;
COMMIT;
```

Repeat the active-grant query to confirm it returns no row. A later regrant is a new row, never an update that clears `revoked_at`. Keep database access and change records under the deployment's operator controls; the database provenance identifies the managed operation, not a fictional individual.
