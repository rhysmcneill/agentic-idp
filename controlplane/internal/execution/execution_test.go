package execution_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// noopEnqueuer records enqueued run IDs without needing a live River client.
type noopEnqueuer struct {
	mu       sync.Mutex
	enqueued []uuid.UUID
}

func (n *noopEnqueuer) Enqueue(_ context.Context, runID uuid.UUID) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enqueued = append(n.enqueued, runID)
	return nil
}

func (n *noopEnqueuer) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.enqueued)
}

type fixture struct {
	conn          *sql.DB
	tenantID      uuid.UUID
	actorID       uuid.UUID
	environmentID uuid.UUID
	pipelines     *pipeline.Store
	enqueuer      *noopEnqueuer
	store         *execution.Store
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
		TenantID:  tn.ID,
		Type:      identity.ActorHuman,
		Name:      "test-actor",
		TeamID:    tm.ID,
		TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}
	env, err := environment.NewStore(conn).Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	pipelines := pipeline.NewStore(conn)
	enqueuer := &noopEnqueuer{}
	store := execution.NewStore(conn, pipelines, enqueuer)

	return &fixture{
		conn: conn, tenantID: tn.ID, actorID: a.ID, environmentID: env.ID,
		pipelines: pipelines, enqueuer: enqueuer, store: store,
	}
}

func (f *fixture) createPipeline(t *testing.T, mutating bool) uuid.UUID {
	t.Helper()
	p, err := f.pipelines.Create(context.Background(), f.tenantID, f.environmentID,
		ci.ProviderGitHubActions, ".github/workflows/deploy.yml", nil, mutating)
	if err != nil {
		t.Fatalf("creating prerequisite pipeline: %v", err)
	}
	return p.ID
}

func TestRequest_ReadOnlyMutatingPipeline_Denied(t *testing.T) {
	f := setup(t)
	pipelineID := f.createPipeline(t, true)

	_, err := f.store.Request(context.Background(), execution.RequestParams{
		TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
		PipelineID: pipelineID, Tier: identity.TierReadOnly,
	})
	if !errors.Is(err, execution.ErrPolicyDenied) {
		t.Fatalf("Request: err = %v, want ErrPolicyDenied", err)
	}
	if f.enqueuer.count() != 0 {
		t.Errorf("enqueued %d jobs, want 0", f.enqueuer.count())
	}
}

func TestRequest_ReadOnlyNonMutatingPipeline_AutoQueued(t *testing.T) {
	f := setup(t)
	pipelineID := f.createPipeline(t, false)

	r, err := f.store.Request(context.Background(), execution.RequestParams{
		TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
		PipelineID: pipelineID, Tier: identity.TierReadOnly,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if r.Status != execution.StatusQueued {
		t.Errorf("Status = %q, want %q", r.Status, execution.StatusQueued)
	}
	if f.enqueuer.count() != 1 {
		t.Errorf("enqueued %d jobs, want 1", f.enqueuer.count())
	}
}

func TestRequest_Autonomous_AutoQueued(t *testing.T) {
	f := setup(t)

	for _, mutating := range []bool{true, false} {
		pipelineID := f.createPipeline(t, mutating)
		r, err := f.store.Request(context.Background(), execution.RequestParams{
			TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
			PipelineID: pipelineID, Tier: identity.TierAutonomous,
		})
		if err != nil {
			t.Fatalf("Request(mutating=%v): %v", mutating, err)
		}
		if r.Status != execution.StatusQueued {
			t.Errorf("Request(mutating=%v): Status = %q, want %q", mutating, r.Status, execution.StatusQueued)
		}
	}
}

func TestRequest_HumanInTheLoop_AlwaysAwaitingApproval(t *testing.T) {
	f := setup(t)

	for _, mutating := range []bool{true, false} {
		pipelineID := f.createPipeline(t, mutating)
		r, err := f.store.Request(context.Background(), execution.RequestParams{
			TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
			PipelineID: pipelineID, Tier: identity.TierHumanInTheLoop,
		})
		if err != nil {
			t.Fatalf("Request(mutating=%v): %v", mutating, err)
		}
		if r.Status != execution.StatusAwaitingApproval {
			t.Errorf("Request(mutating=%v): Status = %q, want %q", mutating, r.Status, execution.StatusAwaitingApproval)
		}

		approval, err := f.store.GetApproval(context.Background(), r.ID)
		if err != nil {
			t.Fatalf("GetApproval(mutating=%v): %v", mutating, err)
		}
		if approval.Decision != "" {
			t.Errorf("Request(mutating=%v): approval.Decision = %q, want empty", mutating, approval.Decision)
		}
	}
	if f.enqueuer.count() != 0 {
		t.Errorf("enqueued %d jobs, want 0 (nothing approved yet)", f.enqueuer.count())
	}
}

func TestDecide_Approved_QueuesRun(t *testing.T) {
	f := setup(t)
	pipelineID := f.createPipeline(t, true)
	r, err := f.store.Request(context.Background(), execution.RequestParams{
		TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
		PipelineID: pipelineID, Tier: identity.TierHumanInTheLoop,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	decided, err := f.store.Decide(context.Background(), r.ID, f.actorID, true)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != execution.StatusQueued {
		t.Errorf("Status = %q, want %q", decided.Status, execution.StatusQueued)
	}
	if f.enqueuer.count() != 1 {
		t.Errorf("enqueued %d jobs, want 1", f.enqueuer.count())
	}
}

func TestDecide_Denied_MarksRunDenied(t *testing.T) {
	f := setup(t)
	pipelineID := f.createPipeline(t, true)
	r, err := f.store.Request(context.Background(), execution.RequestParams{
		TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
		PipelineID: pipelineID, Tier: identity.TierHumanInTheLoop,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	decided, err := f.store.Decide(context.Background(), r.ID, f.actorID, false)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != execution.StatusDenied {
		t.Errorf("Status = %q, want %q", decided.Status, execution.StatusDenied)
	}
	if f.enqueuer.count() != 0 {
		t.Errorf("enqueued %d jobs, want 0", f.enqueuer.count())
	}
}

func TestDecide_ConcurrentDoubleDecide_ExactlyOneWins(t *testing.T) {
	f := setup(t)
	pipelineID := f.createPipeline(t, true)
	r, err := f.store.Request(context.Background(), execution.RequestParams{
		TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
		PipelineID: pipelineID, Tier: identity.TierHumanInTheLoop,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = f.store.Decide(context.Background(), r.ID, f.actorID, true)
	}()
	go func() {
		defer wg.Done()
		_, results[1] = f.store.Decide(context.Background(), r.ID, f.actorID, false)
	}()
	wg.Wait()

	successes, alreadyDecided := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, execution.ErrAlreadyDecided):
			alreadyDecided++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || alreadyDecided != 1 {
		t.Fatalf("got %d successes and %d ErrAlreadyDecided, want exactly 1 of each", successes, alreadyDecided)
	}
}

func TestRequest_IdempotencyKeyUniqueness(t *testing.T) {
	f := setup(t)
	pipelineID := f.createPipeline(t, false)
	params := execution.RequestParams{
		TenantID: f.tenantID, ActorID: f.actorID, EnvironmentID: f.environmentID,
		PipelineID: pipelineID, Tier: identity.TierReadOnly, IdempotencyKey: "retry-key-1",
	}

	if _, err := f.store.Request(context.Background(), params); err != nil {
		t.Fatalf("first Request: %v", err)
	}
	if _, err := f.store.Request(context.Background(), params); err == nil {
		t.Fatal("second Request with the same idempotency key: got nil error, want a uniqueness violation")
	}
}

func TestGet_NotFound(t *testing.T) {
	f := setup(t)
	_, err := f.store.Get(context.Background(), f.tenantID, uuid.New())
	if !errors.Is(err, execution.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
}
