// Package actor stores Actor rows — humans and agents as typed principals.
// See docs/DATA-MODEL.md "actors" and docs/AGENT-MODEL.md.
package actor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// ErrNotFound is returned by Get when no actor matches the given ID.
var ErrNotFound = errors.New("actor: not found")

// Status is the whole-identity lifecycle state of an actor. Revoking it kills
// every session at once; killing one session without the others is a
// separate mechanism (revoked_sessions, keyed by jti).
type Status string

// The two actor statuses.
const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
)

// Actor is a human or agent principal.
type Actor struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Type         identity.ActorType
	Name         string
	TeamID       uuid.UUID
	TrustTier    identity.Tier
	AuthorizedBy *uuid.UUID // nil only for the actor bootstrapped by the static admin token
	Status       Status
	ExpiresAt    *time.Time
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

// CreateParams is the input to Create.
type CreateParams struct {
	TenantID     uuid.UUID
	Type         identity.ActorType
	Name         string
	TeamID       uuid.UUID
	TrustTier    identity.Tier
	AuthorizedBy *uuid.UUID
	ExpiresAt    *time.Time

	// IdempotencyKey, when set, lets a retried enrolment find the actor a
	// prior attempt already created instead of creating a second one — see
	// GetByIdempotencyKey.
	IdempotencyKey *string
}

// Store is a Postgres-backed actor repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new actor and returns the stored row. It does not check
// that the issuing actor's own tier permits granting p.TrustTier — that
// authorisation decision belongs to the caller, not the store.
func (s *Store) Create(ctx context.Context, p CreateParams) (Actor, error) {
	if p.TenantID == uuid.Nil {
		return Actor{}, fmt.Errorf("actor: create: tenant id is required")
	}
	if p.TeamID == uuid.Nil {
		return Actor{}, fmt.Errorf("actor: create: team id is required")
	}
	if p.Name == "" {
		return Actor{}, fmt.Errorf("actor: create: name is required")
	}
	if !p.TrustTier.Valid() {
		return Actor{}, fmt.Errorf("actor: create: invalid trust tier %d", p.TrustTier)
	}

	row, err := s.q.CreateActor(ctx, sqlcgen.CreateActorParams{
		TenantID:       p.TenantID,
		Type:           string(p.Type),
		Name:           p.Name,
		TeamID:         p.TeamID,
		TrustTier:      int16(p.TrustTier), // #nosec G115 -- Valid() above guarantees 1-3
		AuthorizedBy:   nullUUID(p.AuthorizedBy),
		ExpiresAt:      nullTime(p.ExpiresAt),
		IdempotencyKey: nullString(p.IdempotencyKey),
	})
	if err != nil {
		return Actor{}, fmt.Errorf("actor: create: %w", err)
	}
	return fromRow(row), nil
}

// Get returns the actor with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Actor, error) {
	row, err := s.q.GetActor(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrNotFound
	}
	if err != nil {
		return Actor{}, fmt.Errorf("actor: get: %w", err)
	}
	return fromRow(row), nil
}

// GetByIdempotencyKey returns the actor authorizedBy previously created
// under idempotencyKey within tenantID, or ErrNotFound if no retry has
// happened yet. Lets a retried enrolment reuse the actor a prior attempt
// created instead of minting a duplicate.
func (s *Store) GetByIdempotencyKey(ctx context.Context, tenantID, authorizedBy uuid.UUID, idempotencyKey string) (Actor, error) {
	row, err := s.q.GetActorByIdempotencyKey(ctx, sqlcgen.GetActorByIdempotencyKeyParams{
		TenantID:       tenantID,
		AuthorizedBy:   nullUUID(&authorizedBy),
		IdempotencyKey: sql.NullString{String: idempotencyKey, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, ErrNotFound
	}
	if err != nil {
		return Actor{}, fmt.Errorf("actor: get by idempotency key: %w", err)
	}
	return fromRow(row), nil
}

// GrantEnvironment scopes actorID to environmentID — the actor_environments
// row Claims.Environments must be a subset of at token-issue time.
func (s *Store) GrantEnvironment(ctx context.Context, actorID, environmentID uuid.UUID) error {
	if err := s.q.GrantActorEnvironment(ctx, sqlcgen.GrantActorEnvironmentParams{
		ActorID:       actorID,
		EnvironmentID: environmentID,
	}); err != nil {
		return fmt.Errorf("actor: grant environment: %w", err)
	}
	return nil
}

// ListEnvironments returns the environment IDs actorID has been granted.
func (s *Store) ListEnvironments(ctx context.Context, actorID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.ListActorEnvironments(ctx, actorID)
	if err != nil {
		return nil, fmt.Errorf("actor: list environments: %w", err)
	}
	return ids, nil
}

func fromRow(row sqlcgen.Actor) Actor {
	a := Actor{
		ID:        row.ID,
		TenantID:  row.TenantID,
		Type:      identity.ActorType(row.Type),
		Name:      row.Name,
		TeamID:    row.TeamID,
		TrustTier: identity.Tier(row.TrustTier),
		Status:    Status(row.Status),
		CreatedAt: row.CreatedAt,
	}
	if row.AuthorizedBy.Valid {
		id := row.AuthorizedBy.UUID
		a.AuthorizedBy = &id
	}
	if row.ExpiresAt.Valid {
		t := row.ExpiresAt.Time
		a.ExpiresAt = &t
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		a.RevokedAt = &t
	}
	return a
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func nullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}
