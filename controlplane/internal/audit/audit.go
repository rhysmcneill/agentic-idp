// Package audit stores AuditEvent rows. Append-only: this package exposes no
// update or delete method. The application DB role itself isn't yet
// restricted to INSERT/SELECT at the grant level — that lands once the role
// exists — so this Go-level restriction is the only enforcement for now.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// ErrNotFound is returned by Get when no event matches the given ID.
var ErrNotFound = errors.New("audit: not found")

// emptyMetadata is used when CreateParams.Metadata is nil, matching the
// column's own DEFAULT '{}'.
var emptyMetadata = json.RawMessage(`{}`)

// Event is a single audited action: actor, action, and (for credential.mint
// events) the tier, environment and TTL the credential was scoped to.
type Event struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	OccurredAt    time.Time
	ActorID       uuid.UUID
	Action        string
	RunID         *uuid.UUID
	Tier          *identity.Tier
	EnvironmentID *uuid.UUID
	TTLSeconds    *int32
	Metadata      json.RawMessage
}

// CreateParams is the input to Create. RunID, Tier, EnvironmentID and
// TTLSeconds are populated for credential.mint events and nil otherwise.
type CreateParams struct {
	TenantID      uuid.UUID
	ActorID       uuid.UUID
	Action        string
	RunID         *uuid.UUID
	Tier          *identity.Tier
	EnvironmentID *uuid.UUID
	TTLSeconds    *int32
	Metadata      json.RawMessage
}

// Store is a Postgres-backed, append-only audit event repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over an open database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new audit event and returns the stored row.
func (s *Store) Create(ctx context.Context, p CreateParams) (Event, error) {
	if p.TenantID == uuid.Nil {
		return Event{}, fmt.Errorf("audit: create: tenant id is required")
	}
	if p.ActorID == uuid.Nil {
		return Event{}, fmt.Errorf("audit: create: actor id is required")
	}
	if p.Action == "" {
		return Event{}, fmt.Errorf("audit: create: action is required")
	}
	if p.Tier != nil && !p.Tier.Valid() {
		return Event{}, fmt.Errorf("audit: create: invalid tier %d", *p.Tier)
	}

	metadata := p.Metadata
	if metadata == nil {
		metadata = emptyMetadata
	}

	row, err := s.q.CreateAuditEvent(ctx, sqlcgen.CreateAuditEventParams{
		TenantID:      p.TenantID,
		ActorID:       p.ActorID,
		Action:        p.Action,
		RunID:         nullUUID(p.RunID),
		Tier:          nullTier(p.Tier),
		EnvironmentID: nullUUID(p.EnvironmentID),
		TtlSeconds:    nullInt32(p.TTLSeconds),
		Metadata:      metadata,
	})
	if err != nil {
		return Event{}, fmt.Errorf("audit: create: %w", err)
	}
	return fromRow(row), nil
}

// Get returns the audit event with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Event, error) {
	row, err := s.q.GetAuditEvent(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, fmt.Errorf("audit: get: %w", err)
	}
	return fromRow(row), nil
}

func fromRow(row sqlcgen.AuditEvent) Event {
	e := Event{
		ID:         row.ID,
		TenantID:   row.TenantID,
		OccurredAt: row.OccurredAt,
		ActorID:    row.ActorID,
		Action:     row.Action,
		Metadata:   row.Metadata,
	}
	if row.RunID.Valid {
		id := row.RunID.UUID
		e.RunID = &id
	}
	if row.Tier.Valid {
		t := identity.Tier(row.Tier.Int16)
		e.Tier = &t
	}
	if row.EnvironmentID.Valid {
		id := row.EnvironmentID.UUID
		e.EnvironmentID = &id
	}
	if row.TtlSeconds.Valid {
		v := row.TtlSeconds.Int32
		e.TTLSeconds = &v
	}
	return e
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func nullTier(t *identity.Tier) sql.NullInt16 {
	if t == nil {
		return sql.NullInt16{}
	}
	return sql.NullInt16{Int16: int16(*t), Valid: true} // #nosec G115 -- Create validates Valid() above
}

func nullInt32(v *int32) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *v, Valid: true}
}
