// Package session stores revoked_sessions rows and implements
// identity.RevocationChecker against Postgres, combining whole-identity
// revocation (actors.status) with per-session revocation (jti) into the
// single check Verify needs.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/session/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// Session is a single revoked session.
type Session struct {
	Jti       uuid.UUID
	ActorID   uuid.UUID
	RevokedAt time.Time
	ExpiresAt time.Time
}

// Store is a Postgres-backed session repository. It implements
// identity.RevocationChecker directly.
type Store struct {
	q *sqlcgen.Queries
}

var _ identity.RevocationChecker = (*Store)(nil)

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Revoke kills one session without touching the actor's other sessions.
// expiresAt should be copied from the token's own exp — a revocation row has
// no reason to outlive the token it revokes.
func (s *Store) Revoke(ctx context.Context, actorID, jti uuid.UUID, expiresAt time.Time) (Session, error) {
	if actorID == uuid.Nil {
		return Session{}, fmt.Errorf("session: revoke: actor id is required")
	}
	if jti == uuid.Nil {
		return Session{}, fmt.Errorf("session: revoke: jti is required")
	}

	row, err := s.q.RevokeSession(ctx, sqlcgen.RevokeSessionParams{
		Jti:       jti,
		ActorID:   actorID,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return Session{}, fmt.Errorf("session: revoke: %w", err)
	}
	return Session{Jti: row.Jti, ActorID: row.ActorID, RevokedAt: row.RevokedAt, ExpiresAt: row.ExpiresAt}, nil
}

// IsRevoked implements identity.RevocationChecker: true if the actor as a
// whole is revoked, or this specific session is.
func (s *Store) IsRevoked(ctx context.Context, actorID, jti string) (bool, error) {
	aid, err := uuid.Parse(actorID)
	if err != nil {
		return false, fmt.Errorf("session: is revoked: parsing actor id: %w", err)
	}
	j, err := uuid.Parse(jti)
	if err != nil {
		return false, fmt.Errorf("session: is revoked: parsing jti: %w", err)
	}

	revoked, err := s.q.IsRevoked(ctx, sqlcgen.IsRevokedParams{ID: aid, Jti: j})
	if err != nil {
		return false, fmt.Errorf("session: is revoked: %w", err)
	}
	return revoked.Valid && revoked.Bool, nil
}

// DeleteExpired removes revoked_sessions rows whose expires_at has passed —
// the underlying token is rejected on expiry alone by then, so the row is
// dead weight. Nothing calls this periodically yet; that lands once
// cmd/server has a scheduler loop to call it from.
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	n, err := s.q.DeleteExpiredSessions(ctx)
	if err != nil {
		return 0, fmt.Errorf("session: delete expired: %w", err)
	}
	return n, nil
}
