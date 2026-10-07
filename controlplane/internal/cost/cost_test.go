package cost_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/cost"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type fixture struct {
	conn          *sql.DB
	tenantID      uuid.UUID
	actorID       uuid.UUID
	environmentID uuid.UUID
	pipelineID    uuid.UUID
	store         *cost.Store
}

func setup(t *testing.T) *fixture {
	t.Helper()
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	a, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorHuman, Name: "test-actor", TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}
	env, err := environment.NewStore(conn).Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}
	p, err := pipeline.NewStore(conn).Create(ctx, tn.ID, env.ID, ci.ProviderGitHubActions, ".github/workflows/deploy.yml", nil, false)
	if err != nil {
		t.Fatalf("creating prerequisite pipeline: %v", err)
	}

	return &fixture{
		conn: conn, tenantID: tn.ID, actorID: a.ID, environmentID: env.ID, pipelineID: p.ID,
		store: cost.NewStore(conn),
	}
}

// createRun inserts a run row directly (bypassing the execution state
// machine, which isn't this package's concern) with the given CI provider
// and terminal timestamps already set.
func (f *fixture) createRun(t *testing.T, ciProvider string, startedAt, finishedAt time.Time) uuid.UUID {
	t.Helper()
	var runID uuid.UUID
	err := f.conn.QueryRowContext(context.Background(), `
		INSERT INTO runs (tenant_id, actor_id, environment_id, pipeline_id, tier, status, ci_provider, started_at, finished_at)
		VALUES ($1, $2, $3, $4, 3, 'succeeded', $5, $6, $7)
		RETURNING id`,
		f.tenantID, f.actorID, f.environmentID, f.pipelineID, ciProvider, startedAt, finishedAt,
	).Scan(&runID)
	if err != nil {
		t.Fatalf("inserting prerequisite run: %v", err)
	}
	return runID
}

func TestCaptureCIDuration_NoRateConfigured_AmountNil(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	started := time.Now().Add(-2 * time.Minute)
	finished := time.Now()
	runID := f.createRun(t, "github_actions", started, finished)

	rc, err := f.store.CaptureCIDuration(ctx, f.tenantID, runID, f.environmentID, "github_actions", started, finished)
	if err != nil {
		t.Fatalf("CaptureCIDuration: %v", err)
	}
	if rc.AmountMicros != nil {
		t.Errorf("expected nil AmountMicros with no rate configured, got %v", *rc.AmountMicros)
	}
	if rc.DurationMS == nil || *rc.DurationMS < 119_000 {
		t.Errorf("expected ~120000ms duration, got %v", rc.DurationMS)
	}
	if rc.Source != cost.SourceCIDuration {
		t.Errorf("expected source %q, got %q", cost.SourceCIDuration, rc.Source)
	}
}

func TestCaptureCIDuration_WithRate_AmountComputed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	started := time.Now().Add(-1 * time.Minute)
	finished := time.Now()
	runID := f.createRun(t, "github_actions", started, finished)

	provider := "github_actions"
	if err := f.store.SetRate(ctx, f.tenantID, nil, &provider, 100, "USD"); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	rc, err := f.store.CaptureCIDuration(ctx, f.tenantID, runID, f.environmentID, "github_actions", started, finished)
	if err != nil {
		t.Fatalf("CaptureCIDuration: %v", err)
	}
	if rc.AmountMicros == nil {
		t.Fatal("expected AmountMicros to be set once a rate is configured")
	}
	wantMin := int64(59_000) * 100 // allow a little scheduling slack below the ~60000ms duration
	if *rc.AmountMicros < wantMin {
		t.Errorf("amount_micros = %d, want at least %d", *rc.AmountMicros, wantMin)
	}
}

func TestCaptureCIDuration_CalledTwice_Idempotent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	started := time.Now().Add(-1 * time.Minute)
	finished := time.Now()
	runID := f.createRun(t, "github_actions", started, finished)

	first, err := f.store.CaptureCIDuration(ctx, f.tenantID, runID, f.environmentID, "github_actions", started, finished)
	if err != nil {
		t.Fatalf("first CaptureCIDuration: %v", err)
	}
	second, err := f.store.CaptureCIDuration(ctx, f.tenantID, runID, f.environmentID, "github_actions", started, finished)
	if err != nil {
		t.Fatalf("second CaptureCIDuration: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected the same row back on a duplicate call, got ids %s and %s", first.ID, second.ID)
	}

	rows, err := f.store.GetForRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetForRun: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected exactly one ci_duration row after two captures, got %d", len(rows))
	}
}

func TestFindRate_Precedence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	started := time.Now().Add(-1 * time.Minute)
	finished := time.Now()

	provider := "github_actions"
	if err := f.store.SetRate(ctx, f.tenantID, nil, &provider, 10, "USD"); err != nil {
		t.Fatalf("SetRate provider-wide: %v", err)
	}
	if err := f.store.SetRate(ctx, f.tenantID, &f.environmentID, &provider, 999, "USD"); err != nil {
		t.Fatalf("SetRate environment+provider: %v", err)
	}

	runID := f.createRun(t, "github_actions", started, finished)
	rc, err := f.store.CaptureCIDuration(ctx, f.tenantID, runID, f.environmentID, "github_actions", started, finished)
	if err != nil {
		t.Fatalf("CaptureCIDuration: %v", err)
	}
	if rc.AmountMicros == nil {
		t.Fatal("expected an amount")
	}
	// A non-trivial check that the more specific (environment+provider) rate
	// of 999 micros/ms won over the provider-wide rate of 10.
	wantMin := *rc.DurationMS * 999 / 2 // generous lower bound, allows for timing slack
	if *rc.AmountMicros < wantMin {
		t.Errorf("amount_micros = %d suggests the coarser rate (10) was used instead of the more specific one (999)", *rc.AmountMicros)
	}
}

func TestSumByActor_AggregatesAcrossRuns(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	started := time.Now().Add(-1 * time.Minute)
	finished := time.Now()

	run1 := f.createRun(t, "github_actions", started, finished)
	run2 := f.createRun(t, "github_actions", started, finished)
	if _, err := f.store.CaptureCIDuration(ctx, f.tenantID, run1, f.environmentID, "github_actions", started, finished); err != nil {
		t.Fatalf("capture run1: %v", err)
	}
	if _, err := f.store.CaptureCIDuration(ctx, f.tenantID, run2, f.environmentID, "github_actions", started, finished); err != nil {
		t.Fatalf("capture run2: %v", err)
	}

	summaries, err := f.store.SumByActor(ctx, f.tenantID, nil, nil, nil)
	if err != nil {
		t.Fatalf("SumByActor: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected one actor summary, got %d", len(summaries))
	}
	if summaries[0].ActorID != f.actorID {
		t.Errorf("actor id = %s, want %s", summaries[0].ActorID, f.actorID)
	}
	if summaries[0].RunCount != 2 {
		t.Errorf("run_count = %d, want 2", summaries[0].RunCount)
	}
}

func TestCaptureCIDuration_FinishedBeforeStarted_Errors(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	started := time.Now()
	finished := started.Add(-1 * time.Minute)
	runID := f.createRun(t, "github_actions", finished, started) // store swapped order directly too, doesn't matter for this check

	if _, err := f.store.CaptureCIDuration(ctx, f.tenantID, runID, f.environmentID, "github_actions", started, finished); err == nil {
		t.Error("expected an error when finishedAt precedes startedAt")
	}
}
