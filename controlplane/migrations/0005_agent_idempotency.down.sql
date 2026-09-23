DROP INDEX actors_authorized_by_idempotency_key_idx;
ALTER TABLE actors DROP COLUMN idempotency_key;
