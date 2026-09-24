// Package workercred stores and verifies worker credentials. A worker never
// decides or takes a governed action — it only polls for jobs another actor
// already authorised and reports facts back — so it isn't an identity.Actor
// and doesn't go through pkg/identity's token flow: a worker credential
// carries no Tier, Team or Delegation, only an environment scope.
package workercred

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred/sqlcgen"
)

// ErrNotFound is returned by Verify when no credential matches the presented
// token, or the matching credential has been revoked.
var ErrNotFound = errors.New("workercred: not found")

// tokenBytes is the amount of randomness in a minted token. High entropy is
// what makes an unsalted, deterministic hash (see hashToken) safe to look up
// by directly — unlike a human password, this token is never guessed.
const tokenBytes = 32

// Credential is a single worker's bearer credential and the environments it
// may claim and report verification jobs for.
type Credential struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	Name         string
	Environments []uuid.UUID
	CreatedAt    time.Time
	RevokedAt    *time.Time
}

// Store is a Postgres-backed worker-credential repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create mints a new worker credential scoped to environmentIDs, requiring
// at least one — Bootstrap below is the zero-environment path.
func (s *Store) Create(ctx context.Context, tenantID uuid.UUID, name string, environmentIDs []uuid.UUID) (Credential, string, error) {
	if len(environmentIDs) == 0 {
		return Credential{}, "", fmt.Errorf("workercred: create: at least one environment is required")
	}
	return s.mint(ctx, tenantID, name, environmentIDs)
}

// Bootstrap gets-or-rotates tenantID's credential named name: mints it with
// zero environments on first call, rotates its token on every later call,
// since only a hash is ever stored. Safe to call on every worker startup.
func (s *Store) Bootstrap(ctx context.Context, tenantID uuid.UUID, name string) (Credential, string, error) {
	if tenantID == uuid.Nil {
		return Credential{}, "", fmt.Errorf("workercred: bootstrap: tenant id is required")
	}
	if name == "" {
		return Credential{}, "", fmt.Errorf("workercred: bootstrap: name is required")
	}

	existing, err := s.q.GetWorkerCredentialByTenantAndName(ctx, sqlcgen.GetWorkerCredentialByTenantAndNameParams{
		TenantID: tenantID,
		Name:     name,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return s.mint(ctx, tenantID, name, nil)
	}
	if err != nil {
		return Credential{}, "", fmt.Errorf("workercred: bootstrap: %w", err)
	}

	token, err := generateToken()
	if err != nil {
		return Credential{}, "", fmt.Errorf("workercred: bootstrap: %w", err)
	}
	row, err := s.q.RotateWorkerCredentialToken(ctx, sqlcgen.RotateWorkerCredentialTokenParams{
		ID:        existing.ID,
		TokenHash: hashToken(token),
	})
	if err != nil {
		return Credential{}, "", fmt.Errorf("workercred: bootstrap: rotating token: %w", err)
	}

	envIDs, err := s.q.ListWorkerCredentialEnvironments(ctx, row.ID)
	if err != nil {
		return Credential{}, "", fmt.Errorf("workercred: bootstrap: listing environments: %w", err)
	}
	return fromRow(row, envIDs), token, nil
}

// List returns every worker credential belonging to tenantID, ordered by
// name, each with its granted environments — so an operator can find a
// credential's ID by the name they gave it at enrolment/bootstrap time.
func (s *Store) List(ctx context.Context, tenantID uuid.UUID) ([]Credential, error) {
	rows, err := s.q.ListWorkerCredentials(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("workercred: list: %w", err)
	}

	creds := make([]Credential, 0, len(rows))
	for _, row := range rows {
		envIDs, err := s.q.ListWorkerCredentialEnvironments(ctx, row.ID)
		if err != nil {
			return nil, fmt.Errorf("workercred: list: listing environments for %s: %w", row.ID, err)
		}
		creds = append(creds, fromRow(row, envIDs))
	}
	return creds, nil
}

// Grant adds environmentIDs to credentialID's scope. Idempotent.
func (s *Store) Grant(ctx context.Context, credentialID uuid.UUID, environmentIDs []uuid.UUID) error {
	for _, envID := range environmentIDs {
		if err := s.q.GrantWorkerCredentialEnvironment(ctx, sqlcgen.GrantWorkerCredentialEnvironmentParams{
			WorkerCredentialID: credentialID,
			EnvironmentID:      envID,
		}); err != nil {
			return fmt.Errorf("workercred: grant: %w", err)
		}
	}
	return nil
}

// mint creates a fresh credential; environmentIDs may be empty (Bootstrap's
// first-run path).
func (s *Store) mint(ctx context.Context, tenantID uuid.UUID, name string, environmentIDs []uuid.UUID) (Credential, string, error) {
	if tenantID == uuid.Nil {
		return Credential{}, "", fmt.Errorf("workercred: tenant id is required")
	}
	if name == "" {
		return Credential{}, "", fmt.Errorf("workercred: name is required")
	}

	token, err := generateToken()
	if err != nil {
		return Credential{}, "", fmt.Errorf("workercred: %w", err)
	}

	row, err := s.q.CreateWorkerCredential(ctx, sqlcgen.CreateWorkerCredentialParams{
		TenantID:  tenantID,
		Name:      name,
		TokenHash: hashToken(token),
	})
	if err != nil {
		return Credential{}, "", fmt.Errorf("workercred: %w", err)
	}

	if err := s.Grant(ctx, row.ID, environmentIDs); err != nil {
		return Credential{}, "", err
	}
	return fromRow(row, environmentIDs), token, nil
}

// Get returns the credential with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Credential, error) {
	row, err := s.q.GetWorkerCredential(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("workercred: get: %w", err)
	}

	envIDs, err := s.q.ListWorkerCredentialEnvironments(ctx, row.ID)
	if err != nil {
		return Credential{}, fmt.Errorf("workercred: get: listing environments: %w", err)
	}
	return fromRow(row, envIDs), nil
}

// Verify looks up the credential matching token and returns it, or
// ErrNotFound if no live (non-revoked) credential matches.
func (s *Store) Verify(ctx context.Context, token string) (Credential, error) {
	row, err := s.q.GetWorkerCredentialByTokenHash(ctx, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("workercred: verify: %w", err)
	}

	envIDs, err := s.q.ListWorkerCredentialEnvironments(ctx, row.ID)
	if err != nil {
		return Credential{}, fmt.Errorf("workercred: verify: listing environments: %w", err)
	}

	return fromRow(row, envIDs), nil
}

// PermitsEnvironment reports whether c is scoped to environmentID.
func (c Credential) PermitsEnvironment(environmentID uuid.UUID) bool {
	for _, id := range c.Environments {
		if id == environmentID {
			return true
		}
	}
	return false
}

// generateToken returns a fresh, high-entropy bearer token.
func generateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken deterministically hashes token for lookup. Safe without a
// per-token salt only because generateToken's output is high-entropy and
// never guessable — the same reasoning GitHub PATs and Kubernetes bootstrap
// tokens rely on for a bearer credential with no separate identifier to key
// a lookup by first.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func fromRow(row sqlcgen.WorkerCredential, environmentIDs []uuid.UUID) Credential {
	c := Credential{
		ID:           row.ID,
		TenantID:     row.TenantID,
		Name:         row.Name,
		Environments: environmentIDs,
		CreatedAt:    row.CreatedAt,
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		c.RevokedAt = &t
	}
	return c
}
