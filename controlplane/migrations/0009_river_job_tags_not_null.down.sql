-- Vendored from github.com/riverqueue/river/riverdriver/riverpgxv5 v0.47.0
-- (rivermigrate/migration/main/003_river_job_tags_non_null), MPL-2.0 licensed. Reverse of
-- the matching .up.sql — see Decision 023.

ALTER TABLE river_job
    ALTER COLUMN tags DROP NOT NULL,
    ALTER COLUMN tags DROP DEFAULT;
