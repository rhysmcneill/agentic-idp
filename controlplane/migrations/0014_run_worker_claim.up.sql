-- The run row already is the job record for the remote worker's claim/report
-- loop (unlike environment_verifications, which predates a natural row to
-- attach claim state to) — these two columns are all that's needed to extend
-- the same FOR UPDATE SKIP LOCKED claim pattern to runs.
ALTER TABLE runs
    ADD COLUMN claimed_by uuid NULL REFERENCES worker_credentials ON DELETE RESTRICT,
    ADD COLUMN claimed_at timestamptz NULL;
