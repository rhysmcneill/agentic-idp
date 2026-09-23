-- A worker never decides or takes a governed action, only executes jobs
-- another actor already authorised, so it isn't an actors row: it
-- authenticates with its own hashed bearer credential, carrying no tier,
-- team or delegation.
CREATE TABLE worker_credentials (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    name       text NOT NULL,
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz NULL,
    UNIQUE (tenant_id, name)
);

-- Mirrors actor_environments' shape without inheriting actor semantics.
CREATE TABLE worker_credential_environments (
    worker_credential_id uuid NOT NULL REFERENCES worker_credentials ON DELETE CASCADE,
    environment_id        uuid NOT NULL REFERENCES environments ON DELETE RESTRICT,
    PRIMARY KEY (worker_credential_id, environment_id)
);

-- A small, single-purpose queue for one job type (an sts:AssumeRole
-- connectivity check), deliberately not a general run state machine and job
-- queue for arbitrary pipeline executions.
CREATE TABLE environment_verifications (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    environment_id uuid NOT NULL REFERENCES environments ON DELETE RESTRICT,
    status         text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    requested_by   uuid NOT NULL REFERENCES actors ON DELETE RESTRICT,
    claimed_by     uuid NULL REFERENCES worker_credentials ON DELETE RESTRICT,
    tier_results   jsonb NOT NULL DEFAULT '{}',
    requested_at   timestamptz NOT NULL DEFAULT now(),
    claimed_at     timestamptz NULL,
    completed_at   timestamptz NULL
);
