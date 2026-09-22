// Package tenant stores the Tenant row every other tenant-scoped table
// references. See docs/DATA-MODEL.md "tenants" and Decision 001.
package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant/sqlcgen"
)

// ErrNotFound is returned by Get when no tenant matches the given ID.
var ErrNotFound = errors.New("tenant: not found")

// Tenant is the root of every other tenant-scoped table.
type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Store is a Postgres-backed tenant repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over an open database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new tenant with the given name and returns the stored row.
func (s *Store) Create(ctx context.Context, name string) (Tenant, error) {
	if name == "" {
		return Tenant{}, fmt.Errorf("tenant: create: name is required")
	}

	row, err := s.q.CreateTenant(ctx, name)
	if err != nil {
		return Tenant{}, fmt.Errorf("tenant: create: %w", err)
	}
	return fromRow(row), nil
}

// Get returns the tenant with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Tenant, error) {
	row, err := s.q.GetTenant(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("tenant: get: %w", err)
	}
	return fromRow(row), nil
}

func fromRow(row sqlcgen.Tenant) Tenant {
	return Tenant{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt}
}
