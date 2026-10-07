// Package cost attributes run cost (CI duration x an operator-declared
// rate) to the actor and tier that triggered it — a governance signal, not
// a reconciled cloud bill.
package cost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/cost/sqlcgen"
)

// Source identifies where a run_costs row's figures came from.
type Source string

const (
	// SourceCIDuration is captured automatically from a run's own
	// started_at/finished_at timestamps.
	SourceCIDuration Source = "ci_duration"

	// SourceAgentReported is reserved schema room for a future self-reported
	// agent token/LLM cost — nothing in this package writes it yet.
	SourceAgentReported Source = "agent_reported"
)

// RunCost is one captured cost row against a Run.
type RunCost struct {
	ID           uuid.UUID
	RunID        uuid.UUID
	Source       Source
	DurationMS   *int64
	AmountMicros *int64
	Currency     string
	CapturedAt   time.Time
}

// ActorCostSummary rolls up cost across every Run attributed to one actor.
type ActorCostSummary struct {
	ActorID           uuid.UUID
	TotalAmountMicros *int64
	TotalDurationMS   int64
	RunCount          int64
}

// Store is a Postgres-backed cost repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// CaptureCIDuration idempotently records the ci_duration cost row for a run
// that just reached a terminal state, converting duration to an amount using
// whichever rate matches environmentID/ciProvider (most specific scope
// wins), or leaving the amount null if no rate is configured for that scope.
// A second call for the same run is a no-op, returning the row captured by
// the first call.
func (s *Store) CaptureCIDuration(ctx context.Context, tenantID, runID, environmentID uuid.UUID, ciProvider string, startedAt, finishedAt time.Time) (RunCost, error) {
	durationMS := finishedAt.Sub(startedAt).Milliseconds()
	if durationMS < 0 {
		return RunCost{}, fmt.Errorf("cost: capture ci duration: finishedAt (%s) precedes startedAt (%s)", finishedAt, startedAt)
	}

	var amountMicros sql.NullInt64
	rate, err := s.findRate(ctx, tenantID, environmentID, ciProvider)
	if err != nil {
		return RunCost{}, fmt.Errorf("cost: capture ci duration: finding rate: %w", err)
	}
	currency := "USD"
	if rate != nil {
		amountMicros = sql.NullInt64{Int64: durationMS * rate.RateMicrosPerMs, Valid: true}
		currency = rate.Currency
	}

	row, err := s.q.CreateRunCost(ctx, sqlcgen.CreateRunCostParams{
		RunID:        runID,
		Source:       string(SourceCIDuration),
		DurationMs:   sql.NullInt64{Int64: durationMS, Valid: true},
		AmountMicros: amountMicros,
		Currency:     currency,
	})
	if errors.Is(err, sql.ErrNoRows) {
		// ON CONFLICT ... DO NOTHING: a ci_duration row already exists for
		// this run — return what's already there rather than treating a
		// retried/duplicated call as an error.
		existing, getErr := s.q.GetRunCostByRunAndSource(ctx, sqlcgen.GetRunCostByRunAndSourceParams{
			RunID: runID, Source: string(SourceCIDuration),
		})
		if getErr != nil {
			return RunCost{}, fmt.Errorf("cost: capture ci duration: re-reading existing row: %w", getErr)
		}
		return fromRunCostRow(existing), nil
	}
	if err != nil {
		return RunCost{}, fmt.Errorf("cost: capture ci duration: %w", err)
	}
	return fromRunCostRow(row), nil
}

// GetForRun returns every cost row captured against runID.
func (s *Store) GetForRun(ctx context.Context, runID uuid.UUID) ([]RunCost, error) {
	rows, err := s.q.GetCostsForRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("cost: get for run: %w", err)
	}
	out := make([]RunCost, len(rows))
	for i, row := range rows {
		out[i] = fromRunCostRow(row)
	}
	return out, nil
}

// SumByActor returns one row per actor with a run_costs row captured in
// [from, to), optionally narrowed to one actor. tenantID always scopes it.
func (s *Store) SumByActor(ctx context.Context, tenantID uuid.UUID, actorID *uuid.UUID, from, to *time.Time) ([]ActorCostSummary, error) {
	params := sqlcgen.SumCostByActorParams{TenantID: tenantID}
	if actorID != nil {
		params.ActorID = uuid.NullUUID{UUID: *actorID, Valid: true}
	}
	if from != nil {
		params.CapturedFrom = sql.NullTime{Time: *from, Valid: true}
	}
	if to != nil {
		params.CapturedTo = sql.NullTime{Time: *to, Valid: true}
	}

	rows, err := s.q.SumCostByActor(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("cost: sum by actor: %w", err)
	}
	out := make([]ActorCostSummary, len(rows))
	for i, row := range rows {
		summary := ActorCostSummary{
			ActorID:         row.ActorID,
			TotalDurationMS: row.TotalDurationMs,
			RunCount:        row.RunCount,
		}
		if row.HasAmount {
			amount := row.TotalAmountMicros
			summary.TotalAmountMicros = &amount
		}
		out[i] = summary
	}
	return out, nil
}

// SetRate upserts the rate for the given scope (environmentID and/or
// ciProvider may be nil for a wider-scoped default). rateMicrosPerMS is
// currency per millisecond of CI run time, in micro-units of currency.
func (s *Store) SetRate(ctx context.Context, tenantID uuid.UUID, environmentID *uuid.UUID, ciProvider *string, rateMicrosPerMS int64, currency string) error {
	params := sqlcgen.UpsertCostRateParams{
		TenantID:        tenantID,
		RateMicrosPerMs: rateMicrosPerMS,
		Currency:        currency,
	}
	if environmentID != nil {
		params.EnvironmentID = uuid.NullUUID{UUID: *environmentID, Valid: true}
	}
	if ciProvider != nil {
		params.CiProvider = sql.NullString{String: *ciProvider, Valid: true}
	}
	if _, err := s.q.UpsertCostRate(ctx, params); err != nil {
		return fmt.Errorf("cost: set rate: %w", err)
	}
	return nil
}

func (s *Store) findRate(ctx context.Context, tenantID, environmentID uuid.UUID, ciProvider string) (*sqlcgen.CostRate, error) {
	row, err := s.q.FindCostRate(ctx, sqlcgen.FindCostRateParams{
		TenantID:      tenantID,
		EnvironmentID: uuid.NullUUID{UUID: environmentID, Valid: true},
		CiProvider:    sql.NullString{String: ciProvider, Valid: ciProvider != ""},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("finding rate: %w", err)
	}
	return &row, nil
}

func fromRunCostRow(row sqlcgen.RunCost) RunCost {
	rc := RunCost{
		ID:         row.ID,
		RunID:      row.RunID,
		Source:     Source(row.Source),
		Currency:   row.Currency,
		CapturedAt: row.CapturedAt,
	}
	if row.DurationMs.Valid {
		d := row.DurationMs.Int64
		rc.DurationMS = &d
	}
	if row.AmountMicros.Valid {
		a := row.AmountMicros.Int64
		rc.AmountMicros = &a
	}
	return rc
}
