package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type createWorkerRequest struct {
	Name         string   `json:"name"`
	Environments []string `json:"environments"`
}

type createWorkerResponse struct {
	WorkerCredentialID string `json:"worker_credential_id"`
	Token              string `json:"token"`
}

// handlePostWorkers enrols a new worker credential, scoped to the given
// environments. Restricted to Autonomous-tier actors, same as environment
// registration — a worker credential can read that environment's AWS config
// (account_ref, external_id, trust_anchor, role ARNs), so minting one
// carries similar weight.
func handlePostWorkers(environments *environment.Store, workers *workercred.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("workers: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier != identity.TierAutonomous {
			writeError(w, http.StatusForbidden, codeForbidden, "enrolling a worker requires the autonomous tier")
			return
		}

		var req createWorkerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "name is required")
			return
		}
		if len(req.Environments) == 0 {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "at least one environment is required")
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("workers: parsing tenant id from claims: %w", err))
			return
		}
		actorID, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("workers: parsing actor id from claims: %w", err))
			return
		}

		ctx := r.Context()

		envIDs := make([]uuid.UUID, 0, len(req.Environments))
		for _, name := range req.Environments {
			env, err := environments.GetByName(ctx, tenantID, name)
			if errors.Is(err, environment.ErrNotFound) {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("unknown environment %q", name))
				return
			}
			if err != nil {
				writeInternalError(w, err)
				return
			}
			// Same "cannot grant more than you have" rule as tier, applied
			// to environment scope. The root actor (no Delegation) is exempt.
			if claims.Delegation != nil && !claims.PermitsEnvironment(env.ID.String()) {
				writeError(w, http.StatusForbidden, codeForbidden, fmt.Sprintf("cannot grant environment %q outside your own scope", name))
				return
			}
			envIDs = append(envIDs, env.ID)
		}

		cred, token, err := workers.Create(ctx, tenantID, req.Name, envIDs)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: tenantID,
			ActorID:  actorID,
			Action:   "worker.enrolled",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, createWorkerResponse{WorkerCredentialID: cred.ID.String(), Token: token})
	})
}
