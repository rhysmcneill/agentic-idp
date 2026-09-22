// Package team stores Team rows. Catalog entries need owners and approvals
// need somewhere to route — actors and tenants alone are insufficient.
package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team/sqlcgen"
)

// ErrNotFound is returned by Get when no team matches the given ID.
var ErrNotFound = errors.New("team: not found")

// Team owns catalog entries and routes approvals within a Tenant.
type Team struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Store is a Postgres-backed team repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over an open database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new team under tenantID and returns the stored row.
func (s *Store) Create(ctx context.Context, tenantID uuid.UUID, name string) (Team, error) {
	if tenantID == uuid.Nil {
		return Team{}, fmt.Errorf("team: create: tenant id is required")
	}
	if name == "" {
		return Team{}, fmt.Errorf("team: create: name is required")
	}

	row, err := s.q.CreateTeam(ctx, sqlcgen.CreateTeamParams{TenantID: tenantID, Name: name})
	if err != nil {
		return Team{}, fmt.Errorf("team: create: %w", err)
	}
	return fromRow(row), nil
}

// Get returns the team with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Team, error) {
	row, err := s.q.GetTeam(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Team{}, ErrNotFound
	}
	if err != nil {
		return Team{}, fmt.Errorf("team: get: %w", err)
	}
	return fromRow(row), nil
}

func fromRow(row sqlcgen.Team) Team {
	return Team{ID: row.ID, TenantID: row.TenantID, Name: row.Name, CreatedAt: row.CreatedAt}
}
