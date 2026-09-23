// Package verification stores EnvironmentVerification rows: a small,
// single-purpose queue for one job type (an sts:AssumeRole connectivity
// check), not the general run state machine and job queue a later phase
// will need for arbitrary pipeline executions.
package verification

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/verification/sqlcgen"
)

// ErrNotFound is returned by Get when no verification matches the given ID,
// and by Claim when no pending verification is available.
var ErrNotFound = errors.New("verification: not found")

// Status is the lifecycle state of a verification.
type Status string

// The three verification statuses.
const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// TierResult is the outcome of assuming one tier's role.
type TierResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Verification is a single connectivity-check job against one Environment.
type Verification struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	EnvironmentID uuid.UUID
	Status        Status
	RequestedBy   uuid.UUID
	ClaimedBy     *uuid.UUID
	TierResults   map[string]TierResult
	RequestedAt   time.Time
	ClaimedAt     *time.Time
	CompletedAt   *time.Time
}

// Store is a Postgres-backed verification repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new pending verification for environmentID, requested by
// requestedBy — the "no-op job" a human/agent actor triggers.
func (s *Store) Create(ctx context.Context, tenantID, environmentID, requestedBy uuid.UUID) (Verification, error) {
	if tenantID == uuid.Nil || environmentID == uuid.Nil || requestedBy == uuid.Nil {
		return Verification{}, fmt.Errorf("verification: create: tenant id, environment id and requested by are all required")
	}

	row, err := s.q.CreateVerification(ctx, sqlcgen.CreateVerificationParams{
		TenantID:      tenantID,
		EnvironmentID: environmentID,
		RequestedBy:   requestedBy,
	})
	if err != nil {
		return Verification{}, fmt.Errorf("verification: create: %w", err)
	}
	return fromRow(row)
}

// Get returns the verification with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Verification, error) {
	row, err := s.q.GetVerification(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Verification{}, ErrNotFound
	}
	if err != nil {
		return Verification{}, fmt.Errorf("verification: get: %w", err)
	}
	return fromRow(row)
}

// Claim atomically claims the oldest pending verification scoped to one of
// environmentIDs on behalf of workerCredentialID, or ErrNotFound if none is
// pending. Safe under concurrent pollers: FOR UPDATE SKIP LOCKED means two
// workers (or two poll ticks) never claim the same row.
func (s *Store) Claim(ctx context.Context, workerCredentialID uuid.UUID, environmentIDs []uuid.UUID) (Verification, error) {
	if workerCredentialID == uuid.Nil {
		return Verification{}, fmt.Errorf("verification: claim: worker credential id is required")
	}
	if len(environmentIDs) == 0 {
		return Verification{}, ErrNotFound
	}

	row, err := s.q.ClaimNextPendingVerification(ctx, sqlcgen.ClaimNextPendingVerificationParams{
		ClaimedBy:      uuid.NullUUID{UUID: workerCredentialID, Valid: true},
		EnvironmentIds: environmentIDs,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Verification{}, ErrNotFound
	}
	if err != nil {
		return Verification{}, fmt.Errorf("verification: claim: %w", err)
	}
	return fromRow(row)
}

// Complete records tierResults and marks id succeeded (every tier ok) or
// failed, but only if it is still claimed by workerCredentialID — a worker
// cannot complete a job it didn't claim. Returns ErrNotFound otherwise.
func (s *Store) Complete(ctx context.Context, id, workerCredentialID uuid.UUID, tierResults map[string]TierResult) (Verification, error) {
	allOK := true
	for _, r := range tierResults {
		if !r.OK {
			allOK = false
			break
		}
	}
	status := StatusFailed
	if allOK && len(tierResults) > 0 {
		status = StatusSucceeded
	}

	resultsJSON, err := json.Marshal(tierResults)
	if err != nil {
		return Verification{}, fmt.Errorf("verification: complete: marshalling tier results: %w", err)
	}

	row, err := s.q.CompleteVerification(ctx, sqlcgen.CompleteVerificationParams{
		ID:          id,
		Status:      string(status),
		TierResults: resultsJSON,
		ClaimedBy:   uuid.NullUUID{UUID: workerCredentialID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Verification{}, ErrNotFound
	}
	if err != nil {
		return Verification{}, fmt.Errorf("verification: complete: %w", err)
	}
	return fromRow(row)
}

func fromRow(row sqlcgen.EnvironmentVerification) (Verification, error) {
	v := Verification{
		ID:            row.ID,
		TenantID:      row.TenantID,
		EnvironmentID: row.EnvironmentID,
		Status:        Status(row.Status),
		RequestedBy:   row.RequestedBy,
		RequestedAt:   row.RequestedAt,
	}
	if row.ClaimedBy.Valid {
		id := row.ClaimedBy.UUID
		v.ClaimedBy = &id
	}
	if row.ClaimedAt.Valid {
		t := row.ClaimedAt.Time
		v.ClaimedAt = &t
	}
	if row.CompletedAt.Valid {
		t := row.CompletedAt.Time
		v.CompletedAt = &t
	}
	if len(row.TierResults) > 0 {
		if err := json.Unmarshal(row.TierResults, &v.TierResults); err != nil {
			return Verification{}, fmt.Errorf("verification: unmarshalling tier results: %w", err)
		}
	}
	return v, nil
}
