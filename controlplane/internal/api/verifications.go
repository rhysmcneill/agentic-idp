package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/verification"
)

type createVerificationResponse struct {
	VerificationID string `json:"verification_id"`
	Status         string `json:"status"`
}

// handlePostEnvironmentVerify triggers a connectivity check against the
// named environment — the "no-op job" a human/agent actor kicks off, which a
// worker later claims and executes. Any authenticated actor may trigger one:
// it grants no authority and only queues a check against an environment that
// already exists in the caller's own tenant.
func handlePostEnvironmentVerify(environments *environment.Store, verifications *verification.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("verify: no claims in request context — requireAuth was not applied"))
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("verify: parsing tenant id from claims: %w", err))
			return
		}
		actorID, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("verify: parsing actor id from claims: %w", err))
			return
		}

		ctx := r.Context()

		env, err := environments.GetByName(ctx, tenantID, r.PathValue("name"))
		if errors.Is(err, environment.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown environment")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		v, err := verifications.Create(ctx, tenantID, env.ID, actorID)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID:      tenantID,
			ActorID:       actorID,
			Action:        "environment.verify.requested",
			EnvironmentID: &env.ID,
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, createVerificationResponse{VerificationID: v.ID.String(), Status: string(v.Status)})
	})
}

type getVerificationResponse struct {
	VerificationID string                             `json:"verification_id"`
	Status         string                             `json:"status"`
	TierResults    map[string]verification.TierResult `json:"tier_results,omitempty"`
	RequestedAt    time.Time                          `json:"requested_at"`
	CompletedAt    *time.Time                         `json:"completed_at,omitempty"`
}

// handleGetVerification returns the status and (once completed) per-tier
// results of a verification. idpctl polls this today; a Phase 3 UI surface
// renders the same response as "connectivity status."
func handleGetVerification(environments *environment.Store, verifications *verification.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("verify: no claims in request context — requireAuth was not applied"))
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("verify: parsing tenant id from claims: %w", err))
			return
		}

		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed verification id")
			return
		}

		ctx := r.Context()

		env, err := environments.GetByName(ctx, tenantID, r.PathValue("name"))
		if errors.Is(err, environment.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown environment")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		v, err := verifications.Get(ctx, id)
		if errors.Is(err, verification.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown verification")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}
		// A verification for a different tenant or a different environment
		// than named in the path must read as not-found, not as someone
		// else's data leaking through an id a caller happened to guess.
		if v.TenantID != tenantID || v.EnvironmentID != env.ID {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown verification")
			return
		}

		resp := getVerificationResponse{
			VerificationID: v.ID.String(),
			Status:         string(v.Status),
			TierResults:    v.TierResults,
			RequestedAt:    v.RequestedAt,
			CompletedAt:    v.CompletedAt,
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

type nextVerificationResponse struct {
	VerificationID string            `json:"verification_id"`
	Environment    string            `json:"environment"`
	Provider       string            `json:"provider"`
	AccountRef     string            `json:"account_ref"`
	ExternalID     string            `json:"external_id"`
	TrustAnchor    string            `json:"trust_anchor"`
	RoleARNs       map[string]string `json:"role_arns"`
}

// handleGetNextWorkerVerification lets a worker claim the oldest pending
// verification scoped to its own granted environments, returning everything
// it needs to run the check (the environment's AWS config and per-tier role
// ARNs) in the same response so no second round trip is needed. Responds
// 204 when nothing is pending.
func handleGetNextWorkerVerification(environments *environment.Store, verifications *verification.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred, ok := workerCredentialFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("verify: no worker credential in request context — requireWorkerAuth was not applied"))
			return
		}

		ctx := r.Context()

		v, err := verifications.Claim(ctx, cred.ID, cred.Environments)
		if errors.Is(err, verification.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		env, err := environments.Get(ctx, v.EnvironmentID)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		awsConfig, err := environments.GetAWSConfig(ctx, env.ID)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		roleARNs := make(map[string]string, len(tierNames))
		for name, tier := range tierNames {
			role, err := environments.GetAWSTierRole(ctx, env.ID, tier)
			if err != nil {
				writeInternalError(w, err)
				return
			}
			roleARNs[name] = role.RoleARN
		}

		writeJSON(w, http.StatusOK, nextVerificationResponse{
			VerificationID: v.ID.String(),
			Environment:    env.Name,
			Provider:       string(env.Provider),
			AccountRef:     awsConfig.AccountRef,
			ExternalID:     awsConfig.ExternalID,
			TrustAnchor:    awsConfig.TrustAnchor,
			RoleARNs:       roleARNs,
		})
	})
}

type reportVerificationResultRequest struct {
	TierResults map[string]verification.TierResult `json:"tier_results"`
}

// handlePostWorkerVerificationResult records the outcome of a claimed
// verification. Only the worker credential that claimed it may complete it —
// verifications.Complete enforces that at the query level, so a stolen
// credential for one worker can't forge results for jobs another worker
// claimed.
func handlePostWorkerVerificationResult(verifications *verification.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred, ok := workerCredentialFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("verify: no worker credential in request context — requireWorkerAuth was not applied"))
			return
		}

		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed verification id")
			return
		}

		var req reportVerificationResultRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if len(req.TierResults) == 0 {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "tier_results must not be empty")
			return
		}

		v, err := verifications.Complete(r.Context(), id, cred.ID, req.TierResults)
		if errors.Is(err, verification.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown or unclaimed verification")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, createVerificationResponse{VerificationID: v.ID.String(), Status: string(v.Status)})
	})
}
