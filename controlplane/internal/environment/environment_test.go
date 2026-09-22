package environment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestStore_CreateAndGet(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}

	store := environment.NewStore(conn)
	created, err := store.Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "staging" {
		t.Errorf("Name = %q, want %q", created.Name, "staging")
	}
	if created.Provider != cloud.ProviderAWS {
		t.Errorf("Provider = %q, want %q", created.Provider, cloud.ProviderAWS)
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

func TestStore_Get_NotFound(t *testing.T) {
	conn := dbtest.New(t)
	store := environment.NewStore(conn)

	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, environment.ErrNotFound) {
		t.Errorf("Get on unknown ID: err = %v, want ErrNotFound", err)
	}
}

func TestStore_AWSConfigAndTierRoles(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	envStore := environment.NewStore(conn)
	env, err := envStore.Create(ctx, tn.ID, "prod-us-east-1", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	cfg, err := envStore.CreateAWSConfig(ctx, env.ID, "123456789012", "correct-horse-battery-staple", "arn:aws:iam::123456789012:role/oidc-trust-anchor")
	if err != nil {
		t.Fatalf("CreateAWSConfig: %v", err)
	}
	if cfg.EnvironmentID != env.ID {
		t.Errorf("EnvironmentID = %s, want %s", cfg.EnvironmentID, env.ID)
	}

	gotCfg, err := envStore.GetAWSConfig(ctx, env.ID)
	if err != nil {
		t.Fatalf("GetAWSConfig: %v", err)
	}
	if gotCfg != cfg {
		t.Errorf("GetAWSConfig(%s) = %+v, want %+v", env.ID, gotCfg, cfg)
	}

	for _, tier := range []identity.Tier{identity.TierReadOnly, identity.TierHumanInTheLoop, identity.TierAutonomous} {
		roleARN := "arn:aws:iam::123456789012:role/tier-role"
		created, err := envStore.CreateAWSTierRole(ctx, env.ID, tier, roleARN)
		if err != nil {
			t.Fatalf("CreateAWSTierRole(tier=%d): %v", tier, err)
		}
		if created.Tier != tier {
			t.Errorf("Tier = %d, want %d", created.Tier, tier)
		}

		got, err := envStore.GetAWSTierRole(ctx, env.ID, tier)
		if err != nil {
			t.Fatalf("GetAWSTierRole(tier=%d): %v", tier, err)
		}
		if got != created {
			t.Errorf("GetAWSTierRole(tier=%d) = %+v, want %+v", tier, got, created)
		}
	}
}

func TestStore_GetAWSConfig_NotFound(t *testing.T) {
	conn := dbtest.New(t)
	store := environment.NewStore(conn)

	_, err := store.GetAWSConfig(context.Background(), uuid.New())
	if !errors.Is(err, environment.ErrNotFound) {
		t.Errorf("GetAWSConfig on unknown environment: err = %v, want ErrNotFound", err)
	}
}

func TestStore_GetAWSTierRole_InvalidTier(t *testing.T) {
	conn := dbtest.New(t)
	store := environment.NewStore(conn)

	_, err := store.GetAWSTierRole(context.Background(), uuid.New(), identity.Tier(99))
	if err == nil {
		t.Error("GetAWSTierRole with invalid tier: want error, got nil")
	}
}
