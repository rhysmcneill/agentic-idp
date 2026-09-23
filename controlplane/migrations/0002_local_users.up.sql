-- Local username/password login, kept separate from actors because almost
-- no actor ever has one: agents never authenticate this way, and no more
-- than one human needs to until OIDC exists as an alternative.
CREATE TABLE local_users (
    actor_id      uuid PRIMARY KEY REFERENCES actors ON DELETE CASCADE,
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
