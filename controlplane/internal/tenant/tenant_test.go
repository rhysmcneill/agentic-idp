package tenant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
)

func TestStore_CreateAndGet(t *testing.T) {
	conn := dbtest.New(t)
	store := tenant.NewStore(conn)
	ctx := context.Background()

	created, err := store.Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "acme-corp" {
		t.Errorf("Name = %q, want %q", created.Name, "acme-corp")
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
	store := tenant.NewStore(conn)

	_, err := store.Get(context.Background(), uuid.New())
	if !errors.Is(err, tenant.ErrNotFound) {
		t.Errorf("Get on unknown ID: err = %v, want ErrNotFound", err)
	}
}

func TestStore_Create_EmptyName(t *testing.T) {
	conn := dbtest.New(t)
	store := tenant.NewStore(conn)

	_, err := store.Create(context.Background(), "")
	if err == nil {
		t.Error("Create with empty name: want error, got nil")
	}
}
