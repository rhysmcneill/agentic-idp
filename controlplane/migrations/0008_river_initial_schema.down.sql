-- Vendored from github.com/riverqueue/river/riverdriver/riverpgxv5 v0.47.0
-- (rivermigrate/migration/main/002_initial_schema), MPL-2.0 licensed. Reverse of
-- the matching .up.sql — see Decision 023.

DROP TABLE river_job;
DROP FUNCTION river_job_notify;
DROP TYPE river_job_state;

DROP TABLE river_leader;
