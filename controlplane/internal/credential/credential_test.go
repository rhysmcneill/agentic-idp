package credential_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/credential"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestStore_CreateAndVerify(t *testing.T) {
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
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorHuman, Name: "admin",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	store := credential.NewStore(conn)
	created, err := store.Create(ctx, ag.ID, "admin", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Username != "admin" {
		t.Errorf("Username = %q, want %q", created.Username, "admin")
	}

	gotID, err := store.Verify(ctx, "admin", "correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if gotID != ag.ID {
		t.Errorf("Verify returned actor %s, want %s", gotID, ag.ID)
	}
}

func TestStore_Verify_WrongPassword(t *testing.T) {
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
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorHuman, Name: "admin",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	store := credential.NewStore(conn)
	if _, err := store.Create(ctx, ag.ID, "admin", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Verify(ctx, "admin", "wrong-password"); !errors.Is(err, credential.ErrIncorrectPassword) {
		t.Errorf("got %v, want ErrIncorrectPassword", err)
	}
}

func TestStore_Verify_UnknownUsername(t *testing.T) {
	conn := dbtest.New(t)
	store := credential.NewStore(conn)

	if _, err := store.Verify(context.Background(), "nobody", "whatever"); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestStore_Create_DuplicateUsername(t *testing.T) {
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

	store := credential.NewStore(conn)
	as := actor.NewStore(conn)

	a1, err := as.Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorHuman, Name: "admin1",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating actor 1: %v", err)
	}
	a2, err := as.Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorHuman, Name: "admin2",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating actor 2: %v", err)
	}

	if _, err := store.Create(ctx, a1.ID, "admin", "password-one"); err != nil {
		t.Fatalf("Create (first): %v", err)
	}
	if _, err := store.Create(ctx, a2.ID, "admin", "password-two"); err == nil {
		t.Error("Create with a duplicate username: want error, got nil")
	}
}
