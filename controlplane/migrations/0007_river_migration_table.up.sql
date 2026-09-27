-- Vendored from github.com/riverqueue/river/riverdriver/riverpgxv5 v0.47.0
-- (rivermigrate/migration/main/001_create_river_migration), MPL-2.0 licensed. Kept as its own
-- migration file/transaction to match River's own boundary — see
-- Decision 023. Schema template markers stripped for the default
-- (public) Postgres schema.

CREATE TABLE river_migration(
  id bigserial PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT NOW(),
  version bigint NOT NULL,
  CONSTRAINT version CHECK (version >= 1)
);

CREATE UNIQUE INDEX ON river_migration USING btree(version);
