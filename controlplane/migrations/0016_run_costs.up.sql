-- Per-run cost attribution (governance signal, not a reconciled cloud
-- bill — see docs/DATA-MODEL.md "run_costs"). One row per run per source:
-- ci_duration is captured automatically from the run's own started_at/
-- finished_at timestamps; agent_reported is schema room for a future
-- self-reported agent token/LLM cost, not written by any code yet.
CREATE TABLE run_costs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id        uuid NOT NULL REFERENCES runs ON DELETE RESTRICT,
    source        text NOT NULL CHECK (source IN ('ci_duration', 'agent_reported')),
    duration_ms   bigint NULL,
    amount_micros bigint NULL,
    currency      text NOT NULL DEFAULT 'USD',
    captured_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_costs_run_id_idx ON run_costs (run_id);

-- A duplicated capture call can't double-write the ci_duration row for a run.
CREATE UNIQUE INDEX run_costs_run_id_source_ci_duration_idx
    ON run_costs (run_id) WHERE source = 'ci_duration';
