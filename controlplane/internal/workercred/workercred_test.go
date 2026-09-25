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

func TestStore_Bootstrap_FirstCallMintsZeroEnvironmentCredential(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}

	store := workercred.NewStore(conn)
	created, token, err := store.Bootstrap(ctx, tn.ID, "worker-staging")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if token == "" {
		t.Fatal("Bootstrap returned an empty token")
	}
	if len(created.Environments) != 0 {
		t.Errorf("Environments = %v, want empty on first bootstrap", created.Environments)
	}

	verified, err := store.Verify(ctx, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.ID != created.ID {
		t.Errorf("Verify returned credential %s, want %s", verified.ID, created.ID)
	}
}

func TestStore_Bootstrap_RepeatCallRotatesTokenAndKeepsGrants(t *testing.T) {
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
	first, firstToken, err := store.Bootstrap(ctx, tn.ID, "worker-staging")
	if err != nil {
		t.Fatalf("Bootstrap (first): %v", err)
	}
	if err := store.Grant(ctx, first.ID, []uuid.UUID{env.ID}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	second, secondToken, err := store.Bootstrap(ctx, tn.ID, "worker-staging")
	if err != nil {
		t.Fatalf("Bootstrap (second): %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("Bootstrap minted a new credential %s, want the same row %s", second.ID, first.ID)
	}
	if secondToken == firstToken {
		t.Error("Bootstrap returned the same token twice, want a rotated one")
	}
	if !second.PermitsEnvironment(env.ID) {
		t.Error("Bootstrap's rotation dropped an existing environment grant")
	}

	if _, err := store.Verify(ctx, firstToken); !errors.Is(err, workercred.ErrNotFound) {
		t.Errorf("old token still verifies after rotation: got %v, want ErrNotFound", err)
	}
	if _, err := store.Verify(ctx, secondToken); err != nil {
		t.Errorf("Verify(secondToken): %v", err)
	}
}

func TestStore_List_OrderedByName(t *testing.T) {
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
	if _, _, err := store.Create(ctx, tn.ID, "worker-b", []uuid.UUID{env.ID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := store.Bootstrap(ctx, tn.ID, "worker-a"); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	list, err := store.List(ctx, tn.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d credentials, want 2", len(list))
	}
	if list[0].Name != "worker-a" || list[1].Name != "worker-b" {
		t.Errorf("List order = [%s, %s], want alphabetical [worker-a, worker-b]", list[0].Name, list[1].Name)
	}
	if len(list[0].Environments) != 0 {
		t.Errorf("worker-a Environments = %v, want empty", list[0].Environments)
	}
	if !list[1].PermitsEnvironment(env.ID) {
		t.Error("worker-b should permit its granted environment")
	}
}

func TestStore_Grant_IsIdempotent(t *testing.T) {
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
	created, _, err := store.Create(ctx, tn.ID, "worker-staging", []uuid.UUID{env.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Grant(ctx, created.ID, []uuid.UUID{env.ID}); err != nil {
		t.Fatalf("Grant of an already-granted environment returned an error: %v", err)
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
