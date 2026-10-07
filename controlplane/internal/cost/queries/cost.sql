-- name: CreateRunCost :one
INSERT INTO run_costs (run_id, source, duration_ms, amount_micros, currency)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (run_id) WHERE source = 'ci_duration' DO NOTHING
RETURNING *;

-- name: GetRunCostByRunAndSource :one
SELECT * FROM run_costs WHERE run_id = $1 AND source = $2;

-- name: GetCostsForRun :many
SELECT * FROM run_costs WHERE run_id = $1 ORDER BY captured_at;

-- name: SumCostByActor :many
SELECT runs.actor_id,
       COALESCE(SUM(run_costs.amount_micros), 0)::bigint AS total_amount_micros,
       BOOL_OR(run_costs.amount_micros IS NOT NULL)::bool AS has_amount,
       COALESCE(SUM(run_costs.duration_ms), 0)::bigint AS total_duration_ms,
       COUNT(*)::bigint AS run_count
FROM run_costs
JOIN runs ON runs.id = run_costs.run_id
WHERE runs.tenant_id = @tenant_id
  AND (sqlc.narg(actor_id)::uuid IS NULL OR runs.actor_id = sqlc.narg(actor_id))
  AND (sqlc.narg(captured_from)::timestamptz IS NULL OR run_costs.captured_at >= sqlc.narg(captured_from))
  AND (sqlc.narg(captured_to)::timestamptz IS NULL OR run_costs.captured_at < sqlc.narg(captured_to))
GROUP BY runs.actor_id
ORDER BY total_amount_micros DESC NULLS LAST;

-- name: FindCostRate :one
SELECT * FROM cost_rates
WHERE tenant_id = $1
  AND (environment_id = $2 OR environment_id IS NULL)
  AND (ci_provider = $3 OR ci_provider IS NULL)
ORDER BY (environment_id IS NOT NULL) DESC, (ci_provider IS NOT NULL) DESC
LIMIT 1;

-- name: UpsertCostRate :one
INSERT INTO cost_rates (tenant_id, environment_id, ci_provider, rate_micros_per_ms, currency)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'), COALESCE(ci_provider, ''))
DO UPDATE SET rate_micros_per_ms = EXCLUDED.rate_micros_per_ms, currency = EXCLUDED.currency
RETURNING *;
