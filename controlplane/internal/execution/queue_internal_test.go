package execution

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/ci/githubactions"
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

func TestWorker_Work_AdapterRegistered_DoesNotFailClosed(t *testing.T) {
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

	worker := &Worker{store: store, adapters: ci.NewRegistry(githubactions.New())}
	job := &river.Job[JobArgs]{Args: JobArgs{RunID: r.ID}}
	if err := worker.Work(ctx, job); err != nil {
		t.Fatalf("Work: %v", err)
	}

	got, err := store.Get(ctx, tn.ID, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status == StatusFailed {
		t.Errorf("Status after Work = %q, want anything but %q — a registered provider must not fail closed", got.Status, StatusFailed)
	}
	if got.Status != StatusExecuting {
		t.Errorf("Status after Work = %q, want %q (nothing yet drives it further — that's the worker claim/report loop, not built in this step)", got.Status, StatusExecuting)
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

// setupExecutingRun creates the same prerequisite chain as the tests above,
// requests a run into an Autonomous, non-mutating pipeline (so it auto-queues
// without an approval), then transitions it straight to StatusExecuting —
// the state Claim/ReportResult operate on, same as a real Worker.Work call
// would leave it in just before dispatching.
func setupExecutingRun(t *testing.T) (store *Store, conn *sql.DB, tenantID, environmentID, runID uuid.UUID) {
	t.Helper()
	return setupExecutingRunWithSettings(t, nil)
}

// setupExecutingRunWithSettings is setupExecutingRun with pipeline settings
// (repo, ref) controllable — needed to test ResolveExternalRef's matching.
func setupExecutingRunWithSettings(t *testing.T, settings map[string]string) (store *Store, conn *sql.DB, tenantID, environmentID, runID uuid.UUID) {
	t.Helper()
	conn = dbtest.New(t)
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
	p, err := pipelines.Create(ctx, tn.ID, env.ID, ci.ProviderGitHubActions, ".github/workflows/deploy.yml", settings, false)
	if err != nil {
		t.Fatalf("creating prerequisite pipeline: %v", err)
	}

	store = NewStore(conn, pipelines, nopEnqueuer{})
	r, err := store.Request(ctx, RequestParams{
		TenantID: tn.ID, ActorID: a.ID, EnvironmentID: env.ID, PipelineID: p.ID, Tier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := store.transitionStatus(ctx, r.ID, StatusQueued, StatusExecuting); err != nil {
		t.Fatalf("transitioning to executing: %v", err)
	}
	return store, conn, tn.ID, env.ID, r.ID
}

func newWorkerCredential(t *testing.T, conn *sql.DB, tenantID uuid.UUID, environmentIDs []uuid.UUID, name string) uuid.UUID {
	t.Helper()
	cred, _, err := workercred.NewStore(conn).Create(context.Background(), tenantID, name, environmentIDs)
	if err != nil {
		t.Fatalf("creating prerequisite worker credential: %v", err)
	}
	return cred.ID
}

func TestClaim_ClaimsOldestExecutingRunInScope(t *testing.T) {
	store, conn, tenantID, environmentID, runID := setupExecutingRun(t)
	workerID := newWorkerCredential(t, conn, tenantID, []uuid.UUID{environmentID}, "test-worker")

	claimed, err := store.Claim(context.Background(), workerID, []uuid.UUID{environmentID})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID != runID {
		t.Errorf("Claim: ID = %v, want %v", claimed.ID, runID)
	}
	if claimed.ClaimedBy == nil || *claimed.ClaimedBy != workerID {
		t.Errorf("Claim: ClaimedBy = %v, want %v", claimed.ClaimedBy, workerID)
	}
}

func TestClaim_NoneExecuting(t *testing.T) {
	conn := dbtest.New(t)
	tn, err := tenant.NewStore(conn).Create(context.Background(), "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	env, err := environment.NewStore(conn).Create(context.Background(), tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}
	store := NewStore(conn, pipeline.NewStore(conn), nopEnqueuer{})
	workerID := newWorkerCredential(t, conn, tn.ID, []uuid.UUID{env.ID}, "test-worker")

	_, err = store.Claim(context.Background(), workerID, []uuid.UUID{env.ID})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Claim: err = %v, want ErrNotFound", err)
	}
}

func TestClaim_AlreadyClaimed_NotClaimedAgain(t *testing.T) {
	store, conn, tenantID, environmentID, _ := setupExecutingRun(t)
	workerID := newWorkerCredential(t, conn, tenantID, []uuid.UUID{environmentID}, "test-worker")

	if _, err := store.Claim(context.Background(), workerID, []uuid.UUID{environmentID}); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if _, err := store.Claim(context.Background(), workerID, []uuid.UUID{environmentID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Claim: err = %v, want ErrNotFound (nothing left un-claimed)", err)
	}
}

func TestClaim_OutOfScopeEnvironment_NotClaimed(t *testing.T) {
	store, conn, tenantID, environmentID, _ := setupExecutingRun(t)
	otherEnv, err := environment.NewStore(conn).Create(context.Background(), tenantID, "prod", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating second environment: %v", err)
	}
	workerID := newWorkerCredential(t, conn, tenantID, []uuid.UUID{otherEnv.ID}, "test-worker")

	_, err = store.Claim(context.Background(), workerID, []uuid.UUID{otherEnv.ID})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Claim: err = %v, want ErrNotFound (run's environment %v is not in scope)", err, environmentID)
	}
}

func TestReportResult_Succeeds(t *testing.T) {
	store, conn, tenantID, environmentID, runID := setupExecutingRun(t)
	workerID := newWorkerCredential(t, conn, tenantID, []uuid.UUID{environmentID}, "test-worker")
	if _, err := store.Claim(context.Background(), workerID, []uuid.UUID{environmentID}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	r, err := store.ReportResult(context.Background(), runID, workerID, RunResult{
		Status:        StatusSucceeded,
		CIExternalRef: "42",
		CIURL:         "https://github.com/acme/widgets/actions/runs/42",
		CIRawStatus:   "success",
	})
	if err != nil {
		t.Fatalf("ReportResult: %v", err)
	}
	if r.Status != StatusSucceeded {
		t.Errorf("Status = %q, want %q", r.Status, StatusSucceeded)
	}
	if r.CIExternalRef != "42" {
		t.Errorf("CIExternalRef = %q, want %q", r.CIExternalRef, "42")
	}
	if r.FinishedAt == nil {
		t.Error("FinishedAt is nil, want set for a terminal status")
	}
}

func TestReportResult_WrongWorkerCredential_NotFound(t *testing.T) {
	store, conn, tenantID, environmentID, runID := setupExecutingRun(t)
	claimingWorker := newWorkerCredential(t, conn, tenantID, []uuid.UUID{environmentID}, "claiming-worker")
	otherWorker := newWorkerCredential(t, conn, tenantID, []uuid.UUID{environmentID}, "other-worker")
	if _, err := store.Claim(context.Background(), claimingWorker, []uuid.UUID{environmentID}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	_, err := store.ReportResult(context.Background(), runID, otherWorker, RunResult{Status: StatusSucceeded})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ReportResult from a worker that didn't claim it: err = %v, want ErrNotFound", err)
	}
}

func TestReportResult_NotClaimed_NotFound(t *testing.T) {
	store, conn, tenantID, environmentID, runID := setupExecutingRun(t)
	workerID := newWorkerCredential(t, conn, tenantID, []uuid.UUID{environmentID}, "test-worker")

	_, err := store.ReportResult(context.Background(), runID, workerID, RunResult{Status: StatusSucceeded})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("ReportResult on an unclaimed run: err = %v, want ErrNotFound", err)
	}
}

func TestResolveExternalRef_Success(t *testing.T) {
	store, _, _, environmentID, runID := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets"})

	r, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main",
		"42", "https://github.com/acme/widgets/actions/runs/42")
	if err != nil {
		t.Fatalf("ResolveExternalRef: %v", err)
	}
	if r.ID != runID {
		t.Errorf("ID = %v, want %v", r.ID, runID)
	}
	if r.CIExternalRef != "42" {
		t.Errorf("CIExternalRef = %q, want %q", r.CIExternalRef, "42")
	}
}

func TestResolveExternalRef_NonMatchingRepo_NotFound(t *testing.T) {
	store, _, _, environmentID, _ := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets"})

	_, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID},
		string(ci.ProviderGitHubActions), "acme/other-repo", ".github/workflows/deploy.yml", "refs/heads/main", "42", "url")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveExternalRef_NonMatchingRef_NotFound(t *testing.T) {
	store, _, _, environmentID, _ := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets", "ref": "release"})

	_, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main", "42", "url")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (pipeline targets refs/heads/release, not main)", err)
	}
}

func TestResolveExternalRef_DefaultRefIsMain(t *testing.T) {
	// No "ref" setting at all — normalizeRef must default to refs/heads/main.
	store, _, _, environmentID, runID := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets"})

	r, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main", "42", "url")
	if err != nil {
		t.Fatalf("ResolveExternalRef: %v", err)
	}
	if r.ID != runID {
		t.Errorf("ID = %v, want %v", r.ID, runID)
	}
}

func TestResolveExternalRef_OutOfScopeEnvironment_NotFound(t *testing.T) {
	store, _, _, _, _ := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets"})

	_, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{uuid.New()},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main", "42", "url")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveExternalRef_AmbiguousAcrossEnvironments(t *testing.T) {
	store, conn, tenantID, environmentID, _ := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets"})

	// A second environment with a pipeline sharing the same repo/workflow/ref.
	otherEnv, err := environment.NewStore(conn).Create(context.Background(), tenantID, "prod", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating second environment: %v", err)
	}
	pipelines := pipeline.NewStore(conn)
	p2, err := pipelines.Create(context.Background(), tenantID, otherEnv.ID, ci.ProviderGitHubActions,
		".github/workflows/deploy.yml", map[string]string{"repo": "acme/widgets"}, false)
	if err != nil {
		t.Fatalf("creating second pipeline: %v", err)
	}
	a, err := actor.NewStore(conn).Create(context.Background(), actor.CreateParams{
		TenantID: tenantID, Type: identity.ActorHuman, Name: "test-actor-2", TeamID: mustTeamID(t, conn, tenantID), TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating second actor: %v", err)
	}
	r2, err := store.Request(context.Background(), RequestParams{
		TenantID: tenantID, ActorID: a.ID, EnvironmentID: otherEnv.ID, PipelineID: p2.ID, Tier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("requesting second run: %v", err)
	}
	if _, err := store.transitionStatus(context.Background(), r2.ID, StatusQueued, StatusExecuting); err != nil {
		t.Fatalf("transitioning second run to executing: %v", err)
	}

	_, err = store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID, otherEnv.ID},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main", "42", "url")
	if !errors.Is(err, ErrAmbiguousMatch) {
		t.Errorf("err = %v, want ErrAmbiguousMatch", err)
	}
}

func TestResolveExternalRef_AlreadyResolved_NotFound(t *testing.T) {
	store, _, _, environmentID, _ := setupExecutingRunWithSettings(t, map[string]string{"repo": "acme/widgets"})

	if _, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main", "42", "url"); err != nil {
		t.Fatalf("first ResolveExternalRef: %v", err)
	}

	_, err := store.ResolveExternalRef(context.Background(), []uuid.UUID{environmentID},
		string(ci.ProviderGitHubActions), "acme/widgets", ".github/workflows/deploy.yml", "refs/heads/main", "43", "url2")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("re-resolving an already-resolved run: err = %v, want ErrNotFound", err)
	}
}

// mustTeamID creates a second team for tests needing a distinct actor
// outside the fixture's own single-team setup.
func mustTeamID(t *testing.T, conn *sql.DB, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	tm, err := team.NewStore(conn).Create(context.Background(), tenantID, "platform-2")
	if err != nil {
		t.Fatalf("creating team: %v", err)
	}
	return tm.ID
}

var _ = sql.ErrNoRows // silence unused import if the above ever drops its use
