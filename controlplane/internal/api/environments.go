package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type createEnvironmentRequest struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	Region      string            `json:"region"`
	AccountRef  string            `json:"account_ref"`
	ExternalID  string            `json:"external_id"`
	TrustAnchor string            `json:"trust_anchor"`
	RoleARNs    map[string]string `json:"role_arns"`
}

type createEnvironmentResponse struct {
	EnvironmentID string `json:"environment_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Region        string `json:"region"`
}

// handlePostEnvironments registers an Environment: the account-level AWS
// identity (account, external ID, trust anchor) and the per-tier role ARNs a
// worker assumes into. Restricted to Autonomous-tier actors — this defines
// what a tier role can do in the customer's account, not something a lesser
// tier should be able to shape for itself.
func handlePostEnvironments(environments *environment.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("environments: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier != identity.TierAutonomous {
			writeError(w, http.StatusForbidden, codeForbidden, "registering an environment requires the autonomous tier")
			return
		}

		var req createEnvironmentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "name is required")
			return
		}
		if req.Provider != string(cloud.ProviderAWS) {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "provider must be \"aws\" — no other provider is implemented in v1")
			return
		}
		if req.Region == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "region is required")
			return
		}
		if req.AccountRef == "" || req.ExternalID == "" || req.TrustAnchor == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "account_ref, external_id and trust_anchor are all required")
			return
		}
		roleARNs := make(map[identity.Tier]string, len(req.RoleARNs))
		for name, arn := range req.RoleARNs {
			tier, ok := tierNames[name]
			if !ok {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("unknown tier %q in role_arns", name))
				return
			}
			if arn == "" {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("role_arns[%q] must not be empty", name))
				return
			}
			roleARNs[tier] = arn
		}
		for _, tier := range []identity.Tier{identity.TierReadOnly, identity.TierHumanInTheLoop, identity.TierAutonomous} {
			if _, ok := roleARNs[tier]; !ok {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, "role_arns must include read_only, human_in_the_loop and autonomous")
				return
			}
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("environments: parsing tenant id from claims: %w", err))
			return
		}
		actorID, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("environments: parsing actor id from claims: %w", err))
			return
		}

		ctx := r.Context()

		newEnv, err := environments.Create(ctx, tenantID, req.Name, cloud.ProviderAWS, req.Region)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := environments.CreateAWSConfig(ctx, newEnv.ID, req.AccountRef, req.ExternalID, req.TrustAnchor); err != nil {
			writeInternalError(w, err)
			return
		}

		for tier, arn := range roleARNs {
			if _, err := environments.CreateAWSTierRole(ctx, newEnv.ID, tier, arn); err != nil {
				writeInternalError(w, err)
				return
			}
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: tenantID,
			ActorID:  actorID,
			Action:   "environment.registered",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, createEnvironmentResponse{
			EnvironmentID: newEnv.ID.String(),
			Name:          newEnv.Name,
			Provider:      string(newEnv.Provider),
			Region:        newEnv.Region,
		})
	})
}
