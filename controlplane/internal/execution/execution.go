// Package execution implements the Phase 1 run state machine and its
// River-backed job queue (Decision 023).
package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/policy"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// Run state values, matching the CHECK constraint on runs.status exactly.
const (
	StatusRequested        = "requested"
	StatusPolicyChecked    = "policy_checked"
	StatusDenied           = "denied"
	StatusAwaitingApproval = "awaiting_approval"
	StatusQueued           = "queued"
	StatusExecuting        = "executing"
	StatusSucceeded        = "succeeded"
	StatusFailed           = "failed"
	StatusCancelled        = "cancelled"
	StatusTimedOut         = "timed_out"
)

// ErrNotFound is returned by Get when no run matches.
var ErrNotFound = errors.New("execution: not found")

// ErrPolicyDenied is returned by Request on a policy.Deny; no run row is created.
var ErrPolicyDenied = errors.New("execution: denied by policy")

// ErrAlreadyDecided is returned by Decide when the approval was already decided.
var ErrAlreadyDecided = errors.New("execution: approval already decided")

// ErrAmbiguousMatch is returned by ResolveExternalRef when more than one run
// matches — never guessed at, always failed closed.
var ErrAmbiguousMatch = errors.New("execution: ambiguous match")

// Run is a single execution of a Pipeline, requested by an Actor.
type Run struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	ActorID        uuid.UUID
	EnvironmentID  uuid.UUID
	PipelineID     uuid.UUID
	Tier           identity.Tier
	Status         string
	CIProvider     string
	CIExternalRef  string
	CIRawStatus    string
	CIURL          string
	IdempotencyKey string
	DiffRef        string
	RequestedAt    time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	CreatedAt      time.Time
	ClaimedBy      *uuid.UUID
	ClaimedAt      *time.Time
}

// Approval is one row per Run that reaches StatusAwaitingApproval; its absence is the signal no human check occurred.
type Approval struct {
	ID              uuid.UUID
	RunID           uuid.UUID
	RequestedAt     time.Time
	ApproverActorID *uuid.UUID
	Decision        string
	DecidedAt       *time.Time
}

// Enqueuer dispatches a queued Run to River; a separate interface keeps this file free of the River import.
type Enqueuer interface {
	Enqueue(ctx context.Context, runID uuid.UUID) error
}

// Store is a Postgres-backed run/approval repository and owns the run state machine's transitions.
type Store struct {
	db        sqlcgen.DBTX
	q         *sqlcgen.Queries
	pipelines *pipeline.Store
	enqueuer  Enqueuer
}

// NewStore constructs a Store over db (*sql.DB, or a *sql.Tx to compose with other stores in one transaction).
func NewStore(db sqlcgen.DBTX, pipelines *pipeline.Store, enqueuer Enqueuer) *Store {
	return &Store{db: db, q: sqlcgen.New(db), pipelines: pipelines, enqueuer: enqueuer}
}

// RequestParams describes a new Run to create.
type RequestParams struct {
	TenantID       uuid.UUID
	ActorID        uuid.UUID
	EnvironmentID  uuid.UUID
	PipelineID     uuid.UUID
	Tier           identity.Tier
	IdempotencyKey string
	DiffRef        string
}

// Request applies policy.Check and creates a queued or awaiting-approval Run accordingly, or denies outright.
func (s *Store) Request(ctx context.Context, params RequestParams) (Run, error) {
	if !params.Tier.Valid() {
		return Run{}, fmt.Errorf("execution: request: invalid tier %d", params.Tier)
	}

	p, err := s.pipelines.Get(ctx, params.TenantID, params.PipelineID)
	if err != nil {
		return Run{}, fmt.Errorf("execution: request: loading pipeline: %w", err)
	}

	switch policy.Check(params.Tier, p.Mutating) {
	case policy.Deny:
		return Run{}, ErrPolicyDenied

	case policy.Allow:
		runRow, err := s.q.CreateRun(ctx, sqlcgen.CreateRunParams{
			TenantID:       params.TenantID,
			ActorID:        params.ActorID,
			EnvironmentID:  params.EnvironmentID,
			PipelineID:     params.PipelineID,
			Tier:           tierToInt16(params.Tier),
			Status:         StatusPolicyChecked,
			CiProvider:     string(p.Provider),
			IdempotencyKey: nullString(params.IdempotencyKey),
			DiffRef:        nullString(params.DiffRef),
		})
		if err != nil {
			return Run{}, fmt.Errorf("execution: request: create run: %w", err)
		}

		queuedRow, err := s.q.UpdateRunStatus(ctx, sqlcgen.UpdateRunStatusParams{
			NewStatus: StatusQueued,
			ID:        runRow.ID,
			OldStatus: StatusPolicyChecked,
		})
		if err != nil {
			return Run{}, fmt.Errorf("execution: request: transition to queued: %w", err)
		}

		if err := s.enqueuer.Enqueue(ctx, queuedRow.ID); err != nil {
			return Run{}, fmt.Errorf("execution: request: enqueue: %w", err)
		}
		return fromRunRow(queuedRow), nil

	case policy.RequiresApproval:
		runRow, err := s.createRunWithApproval(ctx, sqlcgen.CreateRunParams{
			TenantID:       params.TenantID,
			ActorID:        params.ActorID,
			EnvironmentID:  params.EnvironmentID,
			PipelineID:     params.PipelineID,
			Tier:           tierToInt16(params.Tier),
			Status:         StatusAwaitingApproval,
			CiProvider:     string(p.Provider),
			IdempotencyKey: nullString(params.IdempotencyKey),
			DiffRef:        nullString(params.DiffRef),
		})
		if err != nil {
			return Run{}, fmt.Errorf("execution: request: %w", err)
		}
		return runRow, nil

	default:
		return Run{}, fmt.Errorf("execution: request: unrecognized policy decision")
	}
}

// createRunWithApproval atomically inserts a Run and its Approval, opening its own tx unless already inside one.
func (s *Store) createRunWithApproval(ctx context.Context, params sqlcgen.CreateRunParams) (Run, error) {
	sqlDB, ok := s.db.(*sql.DB)
	if !ok {
		runRow, err := s.q.CreateRun(ctx, params)
		if err != nil {
			return Run{}, fmt.Errorf("create run: %w", err)
		}
		if _, err := s.q.CreateApproval(ctx, runRow.ID); err != nil {
			return Run{}, fmt.Errorf("create approval: %w", err)
		}
		return fromRunRow(runRow), nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcgen.New(tx)
	runRow, err := q.CreateRun(ctx, params)
	if err != nil {
		return Run{}, fmt.Errorf("create run: %w", err)
	}
	if _, err := q.CreateApproval(ctx, runRow.ID); err != nil {
		return Run{}, fmt.Errorf("create approval: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit: %w", err)
	}
	return fromRunRow(runRow), nil
}

// Decide records approverActorID's decision; a guarded UPDATE ensures only one concurrent caller ever wins.
func (s *Store) Decide(ctx context.Context, runID, approverActorID uuid.UUID, approved bool) (Run, error) {
	decision := "denied"
	if approved {
		decision = "approved"
	}

	_, err := s.q.DecideApproval(ctx, sqlcgen.DecideApprovalParams{
		Decision:        nullString(decision),
		ApproverActorID: uuid.NullUUID{UUID: approverActorID, Valid: true},
		RunID:           runID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrAlreadyDecided
	}
	if err != nil {
		return Run{}, fmt.Errorf("execution: decide: %w", err)
	}

	newStatus := StatusDenied
	if approved {
		newStatus = StatusQueued
	}
	runRow, err := s.q.UpdateRunStatus(ctx, sqlcgen.UpdateRunStatusParams{
		NewStatus: newStatus,
		ID:        runID,
		OldStatus: StatusAwaitingApproval,
	})
	if err != nil {
		return Run{}, fmt.Errorf("execution: decide: transition: %w", err)
	}

	if approved {
		if err := s.enqueuer.Enqueue(ctx, runRow.ID); err != nil {
			return Run{}, fmt.Errorf("execution: decide: enqueue: %w", err)
		}
	}
	return fromRunRow(runRow), nil
}

// Get returns the run with the given ID, scoped to tenantID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID, id uuid.UUID) (Run, error) {
	row, err := s.q.GetRun(ctx, sqlcgen.GetRunParams{TenantID: tenantID, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("execution: get: %w", err)
	}
	return fromRunRow(row), nil
}

// GetApproval returns the approval row for runID, or ErrNotFound if the run
// never reached StatusAwaitingApproval.
func (s *Store) GetApproval(ctx context.Context, runID uuid.UUID) (Approval, error) {
	row, err := s.q.GetApprovalByRunID(ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	if err != nil {
		return Approval{}, fmt.Errorf("execution: get approval: %w", err)
	}
	return fromApprovalRow(row), nil
}

// Claim atomically claims the oldest un-claimed executing run scoped to one
// of environmentIDs on behalf of workerCredentialID, or ErrNotFound if none
// is executing. Mirrors verification.Store.Claim's FOR UPDATE SKIP LOCKED
// pattern — safe under concurrent pollers, since two workers (or two poll
// ticks) never claim the same run.
func (s *Store) Claim(ctx context.Context, workerCredentialID uuid.UUID, environmentIDs []uuid.UUID) (Run, error) {
	if workerCredentialID == uuid.Nil {
		return Run{}, fmt.Errorf("execution: claim: worker credential id is required")
	}
	if len(environmentIDs) == 0 {
		return Run{}, ErrNotFound
	}

	row, err := s.q.ClaimNextExecutingRun(ctx, sqlcgen.ClaimNextExecutingRunParams{
		ClaimedBy:      uuid.NullUUID{UUID: workerCredentialID, Valid: true},
		EnvironmentIds: environmentIDs,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("execution: claim: %w", err)
	}
	return fromRunRow(row), nil
}

// RunResult is the outcome a worker reports for a claimed run.
type RunResult struct {
	Status        string // one of StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut
	CIExternalRef string
	CIURL         string
	CIRawStatus   string
}

// ReportResult records result against runID, but only if it is still claimed
// by workerCredentialID and still executing — a worker cannot complete a run
// it didn't claim, or one that already finished. Returns ErrNotFound otherwise.
func (s *Store) ReportResult(ctx context.Context, runID, workerCredentialID uuid.UUID, result RunResult) (Run, error) {
	row, err := s.q.ReportRunResult(ctx, sqlcgen.ReportRunResultParams{
		NewStatus:     result.Status,
		CiExternalRef: nullString(result.CIExternalRef),
		CiUrl:         nullString(result.CIURL),
		CiRawStatus:   nullString(result.CIRawStatus),
		ID:            runID,
		ClaimedBy:     uuid.NullUUID{UUID: workerCredentialID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("execution: report result: %w", err)
	}
	return fromRunRow(row), nil
}

// ResolveExternalRef finds the single executing, not-yet-resolved run for a
// pipeline matching provider+repo+workflowRef+ref, scoped to environmentIDs,
// and records ciExternalRef/ciURL against it. ErrNotFound if none match,
// ErrAmbiguousMatch if more than one does — e.g. two pipelines registered
// against the same repo, workflow and branch in different environments.
func (s *Store) ResolveExternalRef(ctx context.Context, environmentIDs []uuid.UUID, provider, repo, workflowRef, ref, ciExternalRef, ciURL string) (Run, error) {
	candidates, err := s.q.FindUnresolvedExecutingRuns(ctx, sqlcgen.FindUnresolvedExecutingRunsParams{
		EnvironmentIds: environmentIDs,
		Provider:       provider,
		Repo:           repo,
		WorkflowRef:    workflowRef,
	})
	if err != nil {
		return Run{}, fmt.Errorf("execution: resolve external ref: finding candidates: %w", err)
	}

	var match *sqlcgen.Run
	for i := range candidates {
		p, err := s.pipelines.Get(ctx, candidates[i].TenantID, candidates[i].PipelineID)
		if err != nil {
			return Run{}, fmt.Errorf("execution: resolve external ref: loading pipeline: %w", err)
		}
		if normalizeRef(p.Settings["ref"]) != ref {
			continue
		}
		if match != nil {
			return Run{}, ErrAmbiguousMatch
		}
		match = &candidates[i]
	}
	if match == nil {
		return Run{}, ErrNotFound
	}

	row, err := s.q.ResolveRunExternalRef(ctx, sqlcgen.ResolveRunExternalRefParams{
		ID:            match.ID,
		CiExternalRef: nullString(ciExternalRef),
		CiUrl:         nullString(ciURL),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("execution: resolve external ref: %w", err)
	}
	return fromRunRow(row), nil
}

// normalizeRef expands a bare branch/tag setting (the pipeline's default,
// "main") to the full ref form GitHub's verified claims carry.
func normalizeRef(settingRef string) string {
	if settingRef == "" {
		settingRef = "main"
	}
	if strings.HasPrefix(settingRef, "refs/") {
		return settingRef
	}
	return "refs/heads/" + settingRef
}

// transitionStatus guardedly moves runID from oldStatus to newStatus; 0 rows affected surfaces as sql.ErrNoRows.
func (s *Store) transitionStatus(ctx context.Context, runID uuid.UUID, oldStatus, newStatus string) (Run, error) {
	row, err := s.q.UpdateRunStatus(ctx, sqlcgen.UpdateRunStatusParams{
		NewStatus: newStatus,
		ID:        runID,
		OldStatus: oldStatus,
	})
	if err != nil {
		return Run{}, fmt.Errorf("execution: transition status: %w", err) // wrapped, so errors.Is(err, sql.ErrNoRows) still matches
	}
	return fromRunRow(row), nil
}

func tierToInt16(t identity.Tier) int16 {
	return int16(t) // #nosec G115 -- caller validates t.Valid() first
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func fromRunRow(row sqlcgen.Run) Run {
	r := Run{
		ID:             row.ID,
		TenantID:       row.TenantID,
		ActorID:        row.ActorID,
		EnvironmentID:  row.EnvironmentID,
		PipelineID:     row.PipelineID,
		Tier:           identity.Tier(row.Tier),
		Status:         row.Status,
		CIProvider:     row.CiProvider,
		IdempotencyKey: row.IdempotencyKey.String,
		DiffRef:        row.DiffRef.String,
		RequestedAt:    row.RequestedAt,
		CreatedAt:      row.CreatedAt,
	}
	if row.CiExternalRef.Valid {
		r.CIExternalRef = row.CiExternalRef.String
	}
	if row.CiRawStatus.Valid {
		r.CIRawStatus = row.CiRawStatus.String
	}
	if row.CiUrl.Valid {
		r.CIURL = row.CiUrl.String
	}
	if row.StartedAt.Valid {
		t := row.StartedAt.Time
		r.StartedAt = &t
	}
	if row.FinishedAt.Valid {
		t := row.FinishedAt.Time
		r.FinishedAt = &t
	}
	if row.ClaimedBy.Valid {
		id := row.ClaimedBy.UUID
		r.ClaimedBy = &id
	}
	if row.ClaimedAt.Valid {
		t := row.ClaimedAt.Time
		r.ClaimedAt = &t
	}
	return r
}

func fromApprovalRow(row sqlcgen.Approval) Approval {
	a := Approval{
		ID:          row.ID,
		RunID:       row.RunID,
		RequestedAt: row.RequestedAt,
		Decision:    row.Decision.String,
	}
	if row.ApproverActorID.Valid {
		id := row.ApproverActorID.UUID
		a.ApproverActorID = &id
	}
	if row.DecidedAt.Valid {
		t := row.DecidedAt.Time
		a.DecidedAt = &t
	}
	return a
}
