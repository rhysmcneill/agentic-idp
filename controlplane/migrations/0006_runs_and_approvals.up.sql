CREATE TABLE pipelines (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    environment_id uuid NOT NULL REFERENCES environments ON DELETE RESTRICT,
    provider     text NOT NULL CHECK (provider IN ('github_actions', 'gitlab_ci', 'jenkins', 'atlantis', 'bitbucket_pipelines')),
    workflow_ref text NOT NULL,
    settings     jsonb NOT NULL DEFAULT '{}',
    -- Whether this pipeline can mutate the environment. Drives the ReadOnly
    -- tier's policy check (SECURITY-MODEL.md): ReadOnly may trigger a
    -- non-mutating pipeline (e.g. terraform plan) unattended, never a
    -- mutating one.
    mutating     boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE runs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    actor_id        uuid NOT NULL REFERENCES actors ON DELETE RESTRICT,
    environment_id  uuid NOT NULL REFERENCES environments ON DELETE RESTRICT,
    -- No FK: bindings doesn't exist until Phase 2's catalog lands.
    binding_id      uuid NULL,
    pipeline_id     uuid NOT NULL REFERENCES pipelines ON DELETE RESTRICT,
    tier            smallint NOT NULL CHECK (tier BETWEEN 1 AND 3),
    status          text NOT NULL CHECK (status IN (
        'requested', 'policy_checked', 'denied', 'awaiting_approval',
        'queued', 'executing', 'succeeded', 'failed', 'cancelled', 'timed_out'
    )),
    ci_provider     text NOT NULL,
    ci_external_ref text NULL,
    ci_raw_status   text NULL,
    ci_url          text NULL,
    idempotency_key text NULL,
    diff_ref        text NULL,
    requested_at    timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz NULL,
    finished_at     timestamptz NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX runs_tenant_actor_idempotency_key_idx
    ON runs (tenant_id, actor_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- One row per run that reaches awaiting_approval. A run that auto-executes
-- at ReadOnly/Autonomous tier has no row here at all — absence of a row is
-- the signal that no human check occurred, not an implicit auto-approval.
CREATE TABLE approvals (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id            uuid NOT NULL REFERENCES runs ON DELETE RESTRICT,
    requested_at      timestamptz NOT NULL DEFAULT now(),
    approver_actor_id uuid NULL REFERENCES actors ON DELETE RESTRICT,
    decision          text NULL CHECK (decision IN ('approved', 'denied')),
    decided_at        timestamptz NULL
);

CREATE INDEX approvals_run_id_idx ON approvals (run_id);

-- runs didn't exist when audit_events was created (migration 0001).
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_run_id_fkey FOREIGN KEY (run_id) REFERENCES runs ON DELETE RESTRICT;
