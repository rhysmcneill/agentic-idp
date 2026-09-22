package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestStore_CreateAndGet_Minimal(t *testing.T) {
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
	human, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID:  tn.ID,
		Type:      identity.ActorHuman,
		Name:      "John",
		TeamID:    tm.ID,
		TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	store := audit.NewStore(conn)
	created, err := store.Create(ctx, audit.CreateParams{
		TenantID: tn.ID,
		ActorID:  human.ID,
		Action:   "agent.enrolled",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Action != "agent.enrolled" {
		t.Errorf("Action = %q, want %q", created.Action, "agent.enrolled")
	}
	if created.RunID != nil || created.Tier != nil || created.EnvironmentID != nil || created.TTLSeconds != nil {
		t.Errorf("expected nil optional fields for a non-credential-mint event, got %+v", created)
	}
	if string(created.Metadata) != "{}" {
		t.Errorf("Metadata = %s, want {}", created.Metadata)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID || got.Action != created.Action {
		t.Errorf("Get(%s) = %+v, want %+v", created.ID, got, created)
	}
}

func TestStore_CreateAndGet_CredentialMint(t *testing.T) {
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
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID:  tn.ID,
		Type:      identity.ActorAgent,
		Name:      "claude-code-rhys",
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

	tier := identity.TierAutonomous
	ttl := int32(900)
	meta, err := json.Marshal(map[string]string{"role_arn": "arn:aws:iam::123456789012:role/autonomous"})
	if err != nil {
		t.Fatalf("marshalling metadata: %v", err)
	}

	store := audit.NewStore(conn)
	created, err := store.Create(ctx, audit.CreateParams{
		TenantID:      tn.ID,
		ActorID:       ag.ID,
		Action:        "credential.mint",
		Tier:          &tier,
		EnvironmentID: &env.ID,
		TTLSeconds:    &ttl,
		Metadata:      meta,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Tier == nil || *created.Tier != tier {
		t.Errorf("Tier = %v, want %v", created.Tier, tier)
	}
	if created.EnvironmentID == nil || *created.EnvironmentID != env.ID {
		t.Errorf("EnvironmentID = %v, want %v", created.EnvironmentID, env.ID)
	}
	if created.TTLSeconds == nil || *created.TTLSeconds != ttl {
		t.Errorf("TTLSeconds = %v, want %v", created.TTLSeconds, ttl)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	conn := dbtest.New(t)
	store := audit.NewStore(conn)

	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, audit.ErrNotFound) {
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
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID:  tn.ID,
		Type:      identity.ActorAgent,
		Name:      "claude-code-rhys",
		TeamID:    tm.ID,
		TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	invalid := identity.Tier(99)
	store := audit.NewStore(conn)
	_, err = store.Create(ctx, audit.CreateParams{
		TenantID: tn.ID,
		ActorID:  ag.ID,
		Action:   "credential.mint",
		Tier:     &invalid,
	})
	if err == nil {
		t.Error("Create with invalid tier: want error, got nil")
	}
}
