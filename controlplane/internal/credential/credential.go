// Package credential stores and verifies local username/password logins.
// This exists for exactly one actor today — the one POST /v1/setup creates —
// not as a general-purpose account system.
package credential

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/credential/sqlcgen"
)

// ErrNotFound is returned by GetByUsername when no local user matches.
var ErrNotFound = errors.New("credential: not found")

// ErrIncorrectPassword is returned by Verify when the password doesn't match
// the stored hash.
var ErrIncorrectPassword = errors.New("credential: incorrect password")

// LocalUser is a single local username/password login.
type LocalUser struct {
	ActorID   uuid.UUID
	Username  string
	CreatedAt time.Time
}

// Store is a Postgres-backed local-login repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create hashes password and stores a new local login for actorID.
func (s *Store) Create(ctx context.Context, actorID uuid.UUID, username, password string) (LocalUser, error) {
	if actorID == uuid.Nil {
		return LocalUser{}, fmt.Errorf("credential: create: actor id is required")
	}
	if username == "" {
		return LocalUser{}, fmt.Errorf("credential: create: username is required")
	}
	if password == "" {
		return LocalUser{}, fmt.Errorf("credential: create: password is required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return LocalUser{}, fmt.Errorf("credential: create: hashing password: %w", err)
	}

	row, err := s.q.CreateLocalUser(ctx, sqlcgen.CreateLocalUserParams{
		ActorID:      actorID,
		Username:     username,
		PasswordHash: string(hash),
	})
	if err != nil {
		return LocalUser{}, fmt.Errorf("credential: create: %w", err)
	}
	return LocalUser{ActorID: row.ActorID, Username: row.Username, CreatedAt: row.CreatedAt}, nil
}

// Verify checks password against the stored hash for username, returning the
// matching actor ID. Returns ErrNotFound for an unknown username and
// ErrIncorrectPassword for a wrong password — callers should present both
// identically to a client, so a login attempt can't be used to enumerate
// valid usernames.
func (s *Store) Verify(ctx context.Context, username, password string) (uuid.UUID, error) {
	row, err := s.q.GetLocalUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("credential: verify: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)); err != nil {
		return uuid.Nil, ErrIncorrectPassword
	}
	return row.ActorID, nil
}
