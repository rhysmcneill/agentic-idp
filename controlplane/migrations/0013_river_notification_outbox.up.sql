-- Vendored from github.com/riverqueue/river/riverdriver/riverpgxv5 v0.47.0
-- (rivermigrate/migration/main/007_notification_outbox_sqlite_jsonb_and_sql_cleanup), MPL-2.0 licensed. Kept as its own
-- migration file/transaction to match River's own boundary — see
-- Decision 023. Schema template markers stripped for the default
-- (public) Postgres schema.

--
-- Notification outbox.
--

CREATE TABLE river_notification (
    id bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    payload text NOT NULL,
    topic text NOT NULL,
    CONSTRAINT topic_length CHECK (length(topic) > 0 AND length(topic) < 128)
);

CREATE INDEX river_notification_created_at_idx ON river_notification (created_at);
CREATE INDEX river_notification_topic_id_idx ON river_notification (topic, id);

--
-- SQLite JSONB conversion.
--
-- No-op. PostgreSQL already stores River JSON columns as jsonb.

--
-- SQL cleanup.
--

--
-- Drop unused tables `river_client` and `river_client_queue`.
--

DROP TABLE river_client_queue;
DROP TABLE river_client;

--
-- Adds `DEFAULT 25` to `river_job.max_attempts`.
--

ALTER TABLE river_job
    ALTER COLUMN max_attempts SET DEFAULT 25;

--
-- Changes `river_queue.updated_at` to have a default of `CURRENT_TIMESTAMP`.
--

ALTER TABLE river_queue
    ALTER COLUMN updated_at SET DEFAULT CURRENT_TIMESTAMP;
