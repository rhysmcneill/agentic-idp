-- A singleton table: the `id` CHECK permits only the single row `id = true`,
-- so there is exactly one signing key for a deployment, enforced by the
-- schema rather than by application discipline. No rotation yet — that's a
-- second row plus a "current" flag, added when actually needed.
CREATE TABLE signing_keys (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    private_key bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
