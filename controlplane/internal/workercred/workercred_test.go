package workercred_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
)

func TestStore_CreateAndVerify(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	env, err := environment.NewStore(conn).Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	store := workercred.NewStore(conn)
	created, token, err := store.Create(ctx, tn.ID, "worker-staging", []uuid.UUID{env.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if token == "" {
		t.Fatal("Create returned an empty token")
	}
	if created.Name != "worker-staging" {
		t.Errorf("Name = %q, want %q", created.Name, "worker-staging")
	}

	verified, err := store.Verify(ctx, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.ID != created.ID {
		t.Errorf("Verify returned credential %s, want %s", verified.ID, created.ID)
	}
	if !verified.PermitsEnvironment(env.ID) {
		t.Error("PermitsEnvironment returned false for a granted environment")
	}
}

func TestStore_Verify_UnknownToken(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	store := workercred.NewStore(conn)
	if _, err := store.Verify(ctx, "not-a-real-token"); !errors.Is(err, workercred.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestStore_Create_RequiresAtLeastOneEnvironment(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}

	store := workercred.NewStore(conn)
	if _, _, err := store.Create(ctx, tn.ID, "worker-staging", nil); err == nil {
		t.Fatal("Create succeeded with no environments, want an error")
	}
}

func TestCredential_PermitsEnvironment_FalseForUngranted(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	envStore := environment.NewStore(conn)
	granted, err := envStore.Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}
	ungranted, err := envStore.Create(ctx, tn.ID, "prod", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	store := workercred.NewStore(conn)
	created, _, err := store.Create(ctx, tn.ID, "worker-staging", []uuid.UUID{granted.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.PermitsEnvironment(ungranted.ID) {
		t.Error("PermitsEnvironment returned true for an environment not granted")
	}
}
