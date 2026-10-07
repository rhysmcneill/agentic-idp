ALTER TABLE tenants
    ADD COLUMN telemetry_enabled boolean NOT NULL DEFAULT false;

-- Operator-declared rate: currency per unit of CI run TIME (not a flat
-- per-run fee, not a CI-platform subscription cost) — e.g. "this provider's
-- compute costs me $0.008/minute." Scoped per tenant, optionally narrowed to
-- one environment and/or one CI provider — the most specific matching row
-- wins. No matching row = no rate configured: duration is still captured,
-- amount_micros stays NULL. The operator sources this number themselves
-- (their own CI billing page's per-minute overage rate, or their own
-- self-hosted infra cost ÷ capacity) — nothing in this system can discover
-- or fetch it, since it depends on the operator's own plan/infra, not on any
-- publicly queryable API.
CREATE TABLE cost_rates (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          uuid NOT NULL REFERENCES tenants ON DELETE RESTRICT,
    environment_id     uuid NULL REFERENCES environments ON DELETE RESTRICT,
    ci_provider        text NULL,
    rate_micros_per_ms bigint NOT NULL CHECK (rate_micros_per_ms >= 0),
    currency           text NOT NULL DEFAULT 'USD',
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX cost_rates_scope_idx ON cost_rates (
    tenant_id,
    COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'),
    COALESCE(ci_provider, '')
);

-- Singleton random identifier for opt-in telemetry payloads, deliberately
-- separate from tenant_id so the reported instance identity isn't the same
-- value used anywhere else in the system.
CREATE TABLE telemetry_instance (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    singleton  boolean NOT NULL DEFAULT true UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (singleton)
);
