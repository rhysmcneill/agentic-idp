package verification_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/verification"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// fixture sets up a tenant, an environment, a requesting actor and a worker
// credential scoped to that environment — the common prerequisites every
// test here needs.
type fixture struct {
	conn               *sql.DB
	tenantID           uuid.UUID
	environmentID      uuid.UUID
	requestedByActorID uuid.UUID
	workerCredentialID uuid.UUID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "default")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	ac, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorHuman, Name: "admin",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}
	env, err := environment.NewStore(conn).Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}
	wc, _, err := workercred.NewStore(conn).Create(ctx, tn.ID, "worker-staging", []uuid.UUID{env.ID})
	if err != nil {
		t.Fatalf("creating prerequisite worker credential: %v", err)
	}

	return fixture{
		conn:               conn,
		tenantID:           tn.ID,
		environmentID:      env.ID,
		requestedByActorID: ac.ID,
		workerCredentialID: wc.ID,
	}
}

func TestStore_CreateAndGet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := verification.NewStore(f.conn)

	created, err := store.Create(ctx, f.tenantID, f.environmentID, f.requestedByActorID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != verification.StatusPending {
		t.Errorf("Status = %q, want %q", created.Status, verification.StatusPending)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get returned %s, want %s", got.ID, created.ID)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	f := newFixture(t)
	store := verification.NewStore(f.conn)

	if _, err := store.Get(context.Background(), uuid.New()); !errors.Is(err, verification.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestStore_ClaimAndComplete_Succeeded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := verification.NewStore(f.conn)

	created, err := store.Create(ctx, f.tenantID, f.environmentID, f.requestedByActorID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, err := store.Claim(ctx, f.workerCredentialID, []uuid.UUID{f.environmentID})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID != created.ID {
		t.Errorf("Claim returned %s, want %s", claimed.ID, created.ID)
	}
	if claimed.ClaimedBy == nil || *claimed.ClaimedBy != f.workerCredentialID {
		t.Errorf("ClaimedBy = %v, want %s", claimed.ClaimedBy, f.workerCredentialID)
	}

	completed, err := store.Complete(ctx, created.ID, f.workerCredentialID, map[string]verification.TierResult{
		"read_only":         {OK: true},
		"human_in_the_loop": {OK: true},
		"autonomous":        {OK: true},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Status != verification.StatusSucceeded {
		t.Errorf("Status = %q, want %q", completed.Status, verification.StatusSucceeded)
	}
}

func TestStore_Complete_AnyTierFailedMeansOverallFailed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := verification.NewStore(f.conn)

	created, err := store.Create(ctx, f.tenantID, f.environmentID, f.requestedByActorID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Claim(ctx, f.workerCredentialID, []uuid.UUID{f.environmentID}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	completed, err := store.Complete(ctx, created.ID, f.workerCredentialID, map[string]verification.TierResult{
		"read_only":  {OK: true},
		"autonomous": {OK: false, Error: "AccessDenied"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Status != verification.StatusFailed {
		t.Errorf("Status = %q, want %q", completed.Status, verification.StatusFailed)
	}
}

func TestStore_Claim_NoneScoped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := verification.NewStore(f.conn)

	if _, err := store.Create(ctx, f.tenantID, f.environmentID, f.requestedByActorID); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A worker scoped to a different environment must not see this job.
	otherEnv, err := environment.NewStore(f.conn).Create(ctx, f.tenantID, "prod", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating other environment: %v", err)
	}

	if _, err := store.Claim(ctx, f.workerCredentialID, []uuid.UUID{otherEnv.ID}); !errors.Is(err, verification.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestStore_Claim_DoubleClaimNotPossible(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := verification.NewStore(f.conn)

	if _, err := store.Create(ctx, f.tenantID, f.environmentID, f.requestedByActorID); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Claim(ctx, f.workerCredentialID, []uuid.UUID{f.environmentID}); err != nil {
		t.Fatalf("first Claim: %v", err)
	}

	// No more pending jobs — a second worker (or poll tick) claiming again
	// must find nothing, not the same row twice.
	if _, err := store.Claim(ctx, f.workerCredentialID, []uuid.UUID{f.environmentID}); !errors.Is(err, verification.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestStore_Complete_WrongClaimant(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := verification.NewStore(f.conn)

	created, err := store.Create(ctx, f.tenantID, f.environmentID, f.requestedByActorID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Claim(ctx, f.workerCredentialID, []uuid.UUID{f.environmentID}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	imposter := uuid.New()
	if _, err := store.Complete(ctx, created.ID, imposter, map[string]verification.TierResult{"read_only": {OK: true}}); !errors.Is(err, verification.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound (a credential that didn't claim the job must not be able to complete it)", err)
	}
}
