CREATE TABLE tenants (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE teams (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE environments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    name       text NOT NULL,
    provider   text NOT NULL DEFAULT 'aws' CHECK (provider IN ('aws', 'gcp', 'azure')),
    region     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE actors (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    type           text NOT NULL CHECK (type IN ('human', 'agent')),
    name           text NOT NULL,
    team_id        uuid NOT NULL REFERENCES teams ON DELETE RESTRICT,
    trust_tier     smallint NOT NULL CHECK (trust_tier BETWEEN 1 AND 3),
    authorized_by  uuid NULL REFERENCES actors ON DELETE RESTRICT,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    expires_at     timestamptz NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    revoked_at     timestamptz NULL
);

CREATE TABLE actor_environments (
    actor_id       uuid NOT NULL REFERENCES actors ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES environments ON DELETE RESTRICT,
    PRIMARY KEY (actor_id, environment_id)
);

-- 1:1 with environments where environments.provider = 'aws'. external_id is
-- plain text — not a bearer credential on its own, kept out of API
-- responses rather than encrypted.
CREATE TABLE environment_aws_config (
    environment_id uuid PRIMARY KEY REFERENCES environments ON DELETE CASCADE,
    account_ref    text NOT NULL,
    external_id    text NOT NULL,
    trust_anchor   text NOT NULL
);

CREATE TABLE environment_aws_tier_roles (
    environment_id uuid NOT NULL REFERENCES environment_aws_config ON DELETE CASCADE,
    tier           smallint NOT NULL CHECK (tier BETWEEN 1 AND 3),
    role_arn       text NOT NULL,
    PRIMARY KEY (environment_id, tier)
);

-- Append-only: revoked at the schema level via privileges in a later
-- migration once the application DB role exists, per CLAUDE.md's audit
-- invariant. No updated_at, deliberately.
CREATE TABLE audit_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    actor_id       uuid NOT NULL REFERENCES actors ON DELETE RESTRICT,
    action         text NOT NULL,
    -- No FK yet: `runs` doesn't exist until Phase 1's migration adds it.
    run_id         uuid NULL,
    tier           smallint NULL CHECK (tier BETWEEN 1 AND 3),
    environment_id uuid NULL REFERENCES environments ON DELETE RESTRICT,
    ttl_seconds    integer NULL,
    metadata       jsonb NOT NULL DEFAULT '{}'
);

-- See docs/DATA-MODEL.md "Session and revocation": a row here means "reject
-- this session's token even though the signature and exp are still valid."
CREATE TABLE revoked_sessions (
    jti        uuid PRIMARY KEY,
    actor_id   uuid NOT NULL REFERENCES actors ON DELETE CASCADE,
    revoked_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
