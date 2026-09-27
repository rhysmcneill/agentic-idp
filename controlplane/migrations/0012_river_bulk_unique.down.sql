-- Vendored from github.com/riverqueue/river/riverdriver/riverpgxv5 v0.47.0
-- (rivermigrate/migration/main/006_bulk_unique), MPL-2.0 licensed. Reverse of
-- the matching .up.sql — see Decision 023.


--
-- Drop `river_job.unique_states` and its index.
--

DROP INDEX river_job_unique_idx;

ALTER TABLE river_job
    DROP COLUMN unique_states;

CREATE UNIQUE INDEX IF NOT EXISTS river_job_kind_unique_key_idx ON river_job (kind, unique_key) WHERE unique_key IS NOT NULL;

--
-- Drop `river_job_state_in_bitmask` function.
--
DROP FUNCTION river_job_state_in_bitmask;
