package pipeline_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
)

func setup(t *testing.T) (conn *sql.DB, tenantID, environmentID uuid.UUID) {
	t.Helper()
	conn = dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	env, err := environment.NewStore(conn).Create(ctx, tn.ID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}
	return conn, tn.ID, env.ID
}

func TestStore_CreateAndGet(t *testing.T) {
	conn, tenantID, environmentID := setup(t)
	ctx := context.Background()
	store := pipeline.NewStore(conn)

	settings := map[string]string{"workflow_file": "deploy.yml"}
	created, err := store.Create(ctx, tenantID, environmentID, ci.ProviderGitHubActions, ".github/workflows/deploy.yml", settings, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("Create returned a nil ID")
	}
	if created.Provider != ci.ProviderGitHubActions {
		t.Errorf("Provider = %q, want %q", created.Provider, ci.ProviderGitHubActions)
	}
	if created.Mutating {
		t.Error("Mutating = true, want false")
	}
	if created.Settings["workflow_file"] != "deploy.yml" {
		t.Errorf("Settings[workflow_file] = %q, want %q", created.Settings["workflow_file"], "deploy.yml")
	}

	got, err := store.Get(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID || got.Mutating != created.Mutating || got.Settings["workflow_file"] != "deploy.yml" {
		t.Errorf("Get(%s) = %+v, want %+v", created.ID, got, created)
	}
}

func TestStore_Create_MutatingDefault(t *testing.T) {
	conn, tenantID, environmentID := setup(t)
	ctx := context.Background()
	store := pipeline.NewStore(conn)

	created, err := store.Create(ctx, tenantID, environmentID, ci.ProviderGitHubActions, ".github/workflows/apply.yml", nil, true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !created.Mutating {
		t.Error("Mutating = false, want true")
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	conn, tenantID, _ := setup(t)
	store := pipeline.NewStore(conn)

	_, err := store.Get(context.Background(), tenantID, uuid.New())
	if !errors.Is(err, pipeline.ErrNotFound) {
		t.Errorf("Get on unknown ID: err = %v, want ErrNotFound", err)
	}
}

func TestStore_Get_WrongTenant(t *testing.T) {
	conn, tenantID, environmentID := setup(t)
	ctx := context.Background()
	store := pipeline.NewStore(conn)

	created, err := store.Create(ctx, tenantID, environmentID, ci.ProviderGitHubActions, ".github/workflows/deploy.yml", nil, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = store.Get(ctx, uuid.New(), created.ID)
	if !errors.Is(err, pipeline.ErrNotFound) {
		t.Errorf("Get with wrong tenant: err = %v, want ErrNotFound", err)
	}
}
