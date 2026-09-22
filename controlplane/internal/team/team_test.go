package team_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
)

func TestStore_CreateAndGet(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tenants := tenant.NewStore(conn)
	tn, err := tenants.Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}

	store := team.NewStore(conn)
	created, err := store.Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "platform" {
		t.Errorf("Name = %q, want %q", created.Name, "platform")
	}
	if created.TenantID != tn.ID {
		t.Errorf("TenantID = %s, want %s", created.TenantID, tn.ID)
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
	store := team.NewStore(conn)

	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, team.ErrNotFound) {
		t.Errorf("Get on unknown ID: err = %v, want ErrNotFound", err)
	}
}

func TestStore_Create_EmptyName(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tenants := tenant.NewStore(conn)
	tn, err := tenants.Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}

	store := team.NewStore(conn)
	_, err = store.Create(ctx, tn.ID, "")
	if err == nil {
		t.Error("Create with empty name: want error, got nil")
	}
}

func TestStore_Create_UnknownTenant(t *testing.T) {
	conn := dbtest.New(t)
	store := team.NewStore(conn)

	_, err := store.Create(context.Background(), uuid.New(), "platform")
	if err == nil {
		t.Error("Create with unknown tenant id: want error (FK violation), got nil")
	}
}
