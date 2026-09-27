-- Vendored from github.com/riverqueue/river/riverdriver/riverpgxv5 v0.47.0
-- (rivermigrate/migration/main/003_river_job_tags_non_null), MPL-2.0 licensed. Kept as its own
-- migration file/transaction to match River's own boundary — see
-- Decision 023. Schema template markers stripped for the default
-- (public) Postgres schema.

ALTER TABLE river_job ALTER COLUMN tags SET DEFAULT '{}';
UPDATE river_job SET tags = '{}' WHERE tags IS NULL;
ALTER TABLE river_job ALTER COLUMN tags SET NOT NULL;
