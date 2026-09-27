package execution

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// nopEnqueuer never enqueues — Work itself is under test here, not Request.
type nopEnqueuer struct{}

func (nopEnqueuer) Enqueue(context.Context, uuid.UUID) error { return nil }

func TestWorker_Work_NoAdapterRegistered_FailsClosed(t *testing.T) {
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
	pipelines := pipeline.NewStore(conn)
	p, err := pipelines.Create(ctx, tn.ID, env.ID, ci.ProviderGitHubActions, ".github/workflows/deploy.yml", nil, true)
	if err != nil {
		t.Fatalf("creating prerequisite pipeline: %v", err)
	}

	store := NewStore(conn, pipelines, nopEnqueuer{})
	r, err := store.Request(ctx, RequestParams{
		TenantID: tn.ID, ActorID: a.ID, EnvironmentID: env.ID, PipelineID: p.ID, Tier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if r.Status != StatusQueued {
		t.Fatalf("Request: Status = %q, want %q", r.Status, StatusQueued)
	}

	worker := &Worker{store: store, adapters: ci.NewRegistry()} // no adapters registered
	job := &river.Job[JobArgs]{Args: JobArgs{RunID: r.ID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("Work: %v", err)
	}

	got, err := store.Get(ctx, tn.ID, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("Status after Work = %q, want %q (no adapter registered should fail closed)", got.Status, StatusFailed)
	}
}

func TestWorker_Work_AlreadyExecuting_IsNoop(t *testing.T) {
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
	pipelines := pipeline.NewStore(conn)
	p, err := pipelines.Create(ctx, tn.ID, env.ID, ci.ProviderGitHubActions, ".github/workflows/deploy.yml", nil, true)
	if err != nil {
		t.Fatalf("creating prerequisite pipeline: %v", err)
	}

	store := NewStore(conn, pipelines, nopEnqueuer{})
	r, err := store.Request(ctx, RequestParams{
		TenantID: tn.ID, ActorID: a.ID, EnvironmentID: env.ID, PipelineID: p.ID, Tier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := store.transitionStatus(ctx, r.ID, StatusQueued, StatusExecuting); err != nil {
		t.Fatalf("pre-transitioning to executing: %v", err)
	}

	worker := &Worker{store: store, adapters: ci.NewRegistry()}
	job := &river.Job[JobArgs]{Args: JobArgs{RunID: r.ID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("Work on an already-executing run: %v", err)
	}

	got, err := store.Get(ctx, tn.ID, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusExecuting {
		t.Errorf("Status = %q, want %q (redelivered job must not disturb an in-flight run)", got.Status, StatusExecuting)
	}
}

var _ = sql.ErrNoRows // silence unused import if the above ever drops its use
