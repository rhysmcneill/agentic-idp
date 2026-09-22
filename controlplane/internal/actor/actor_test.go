package actor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestStore_CreateAndGet_BootstrapActor(t *testing.T) {
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

	store := actor.NewStore(conn)
	created, err := store.Create(ctx, actor.CreateParams{
		TenantID:  tn.ID,
		Type:      identity.ActorHuman,
		Name:      "rhys",
		TeamID:    tm.ID,
		TrustTier: identity.TierAutonomous,
		// AuthorizedBy nil: the actor bootstrapped by the static admin token.
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.AuthorizedBy != nil {
		t.Errorf("AuthorizedBy = %v, want nil for a bootstrap actor", created.AuthorizedBy)
	}
	if created.Status != actor.StatusActive {
		t.Errorf("Status = %q, want %q", created.Status, actor.StatusActive)
	}
	if created.ID == uuid.Nil {
		t.Error("Create returned a nil ID")
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != created {
		t.Errorf("Get(%s) = %+v, want %+v", created.ID, got, created)
	}
}

func TestStore_CreateAndGet_EnrolledAgent(t *testing.T) {
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

	store := actor.NewStore(conn)
	human, err := store.Create(ctx, actor.CreateParams{
		TenantID:  tn.ID,
		Type:      identity.ActorHuman,
		Name:      "rhys",
		TeamID:    tm.ID,
		TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite human actor: %v", err)
	}

	expires := time.Now().Add(24 * time.Hour).Truncate(time.Microsecond)
	agent, err := store.Create(ctx, actor.CreateParams{
		TenantID:     tn.ID,
		Type:         identity.ActorAgent,
		Name:         "claude-code-rhys",
		TeamID:       tm.ID,
		TrustTier:    identity.TierHumanInTheLoop,
		AuthorizedBy: &human.ID,
		ExpiresAt:    &expires,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if agent.AuthorizedBy == nil || *agent.AuthorizedBy != human.ID {
		t.Errorf("AuthorizedBy = %v, want %s", agent.AuthorizedBy, human.ID)
	}
	if agent.ExpiresAt == nil || !agent.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", agent.ExpiresAt, expires)
	}
	if agent.Type != identity.ActorAgent {
		t.Errorf("Type = %q, want %q", agent.Type, identity.ActorAgent)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	conn := dbtest.New(t)
	store := actor.NewStore(conn)

	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, actor.ErrNotFound) {
		t.Errorf("Get on unknown ID: err = %v, want ErrNotFound", err)
	}
}

func TestStore_Create_InvalidTier(t *testing.T) {
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

	store := actor.NewStore(conn)
	_, err = store.Create(ctx, actor.CreateParams{
		TenantID:  tn.ID,
		Type:      identity.ActorHuman,
		Name:      "rhys",
		TeamID:    tm.ID,
		TrustTier: identity.Tier(99),
	})
	if err == nil {
		t.Error("Create with invalid tier: want error, got nil")
	}
}
