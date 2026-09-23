-- Same pattern as runs.idempotency_key: caller-supplied, scoped to who
-- requested it, so a retried enrolment can't create a second actor.
ALTER TABLE actors ADD COLUMN idempotency_key text NULL;

CREATE UNIQUE INDEX actors_authorized_by_idempotency_key_idx
    ON actors (tenant_id, authorized_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
