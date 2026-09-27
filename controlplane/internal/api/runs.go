package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type createRunRequest struct {
	EnvironmentID  string `json:"environment_id"`
	PipelineID     string `json:"pipeline_id"`
	IdempotencyKey string `json:"idempotency_key"`
	DiffRef        string `json:"diff_ref"`
}

type runResponse struct {
	RunID         string `json:"run_id"`
	EnvironmentID string `json:"environment_id"`
	PipelineID    string `json:"pipeline_id"`
	Tier          string `json:"tier"`
	Status        string `json:"status"`
}

// handlePostRuns requests a new Run. Tier and actor come from the
// authenticated actor's verified claims, never from the request body — the
// caller cannot name its own authority (see CLAUDE.md's delegation
// invariant). Returns immediately per the async run model; the caller polls
// GET /v1/runs/{id} for status.
func handlePostRuns(environments *environment.Store, runs *execution.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("runs: no claims in request context — requireAuth was not applied"))
			return
		}

		var req createRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		environmentID, err := uuid.Parse(req.EnvironmentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "environment_id must be a valid UUID")
			return
		}
		pipelineID, err := uuid.Parse(req.PipelineID)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "pipeline_id must be a valid UUID")
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("runs: parsing tenant id from claims: %w", err))
			return
		}
		actorID, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("runs: parsing actor id from claims: %w", err))
			return
		}

		ctx := r.Context()

		env, err := environments.Get(ctx, environmentID)
		if errors.Is(err, environment.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown environment")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if env.TenantID != tenantID {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown environment")
			return
		}

		run, err := runs.Request(ctx, execution.RequestParams{
			TenantID:       tenantID,
			ActorID:        actorID,
			EnvironmentID:  environmentID,
			PipelineID:     pipelineID,
			Tier:           claims.Tier,
			IdempotencyKey: req.IdempotencyKey,
			DiffRef:        req.DiffRef,
		})
		switch {
		case errors.Is(err, execution.ErrPolicyDenied):
			writeError(w, http.StatusForbidden, codePolicyDenied, "the actor's tier does not permit this pipeline")
			return
		case errors.Is(err, pipeline.ErrNotFound):
			writeError(w, http.StatusNotFound, codeNotFound, "unknown pipeline")
			return
		case err != nil:
			writeInternalError(w, err)
			return
		}

		tier := run.Tier
		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID:      tenantID,
			ActorID:       actorID,
			Action:        "run.requested",
			RunID:         &run.ID,
			Tier:          &tier,
			EnvironmentID: &environmentID,
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, runToResponse(run))
	})
}

// handleGetRun returns a single Run, tenant-scoped, for polling — the async
// run model applies here too: the client polls this rather than the request
// blocking on execution.
func handleGetRun(runs *execution.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("runs: no claims in request context — requireAuth was not applied"))
			return
		}
		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("runs: parsing tenant id from claims: %w", err))
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "id must be a valid UUID")
			return
		}

		run, err := runs.Get(r.Context(), tenantID, id)
		if errors.Is(err, execution.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown run")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, runToResponse(run))
	})
}

type decideRunRequest struct {
	Decision string `json:"decision"`
}

// handlePostRunDecision records a human's approve/deny decision on a Run
// awaiting approval. Restricted to HumanInTheLoop and Autonomous tiers — a
// ReadOnly actor, which has no action authority of its own, cannot grant
// another actor's run its approval either.
func handlePostRunDecision(runs *execution.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("runs: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier == identity.TierReadOnly {
			writeError(w, http.StatusForbidden, codeForbidden, "deciding a run requires human_in_the_loop or autonomous tier")
			return
		}

		var req decideRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		var approved bool
		switch req.Decision {
		case "approved":
			approved = true
		case "denied":
			approved = false
		default:
			writeError(w, http.StatusBadRequest, codeInvalidRequest, `decision must be "approved" or "denied"`)
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("runs: parsing tenant id from claims: %w", err))
			return
		}
		actorID, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("runs: parsing actor id from claims: %w", err))
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "id must be a valid UUID")
			return
		}

		ctx := r.Context()

		// Confirms the run belongs to the caller's tenant before Decide acts on
		// it — Decide itself is not tenant-scoped, since a run ID is already an
		// unguessable UUID and approvals are keyed by run, not tenant.
		existing, err := runs.Get(ctx, tenantID, id)
		if errors.Is(err, execution.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown run")
			return
		} else if err != nil {
			writeInternalError(w, err)
			return
		}
		// Separation of duties: the point of HumanInTheLoop is that a
		// different human checks the action, not the requester rubber-stamping
		// its own run.
		if existing.ActorID == actorID {
			writeError(w, http.StatusForbidden, codeForbidden, "an actor may not decide its own run")
			return
		}

		run, err := runs.Decide(ctx, id, actorID, approved)
		switch {
		case errors.Is(err, execution.ErrAlreadyDecided):
			writeError(w, http.StatusConflict, codeConflict, "this run's approval was already decided")
			return
		case err != nil:
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: tenantID,
			ActorID:  actorID,
			Action:   "approval.decided",
			RunID:    &run.ID,
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, runToResponse(run))
	})
}

func runToResponse(r execution.Run) runResponse {
	return runResponse{
		RunID:         r.ID.String(),
		EnvironmentID: r.EnvironmentID.String(),
		PipelineID:    r.PipelineID.String(),
		Tier:          r.Tier.String(),
		Status:        r.Status,
	}
}
