package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
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

		envIDs, ok := resolveWorkerEnvironments(ctx, w, environments, claims, tenantID, req.Environments)
		if !ok {
			return
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

type workerCredentialResponse struct {
	WorkerCredentialID string   `json:"worker_credential_id"`
	Name               string   `json:"name"`
	Environments       []string `json:"environments"`
	RevokedAt          *string  `json:"revoked_at,omitempty"`
}

// handleGetWorkers lists the tenant's worker credentials — how an operator
// finds a self-registered worker's ID by the name they gave it, since
// neither `idpctl worker enrol` nor self-registration surface it again
// afterward except in the worker's own startup log.
func handleGetWorkers(environments *environment.Store, workers *workercred.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("workers: no claims in request context — requireAuth was not applied"))
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("workers: parsing tenant id from claims: %w", err))
			return
		}

		ctx := r.Context()

		creds, err := workers.List(ctx, tenantID)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		resp := make([]workerCredentialResponse, 0, len(creds))
		for _, cred := range creds {
			envNames := make([]string, 0, len(cred.Environments))
			for _, envID := range cred.Environments {
				env, err := environments.Get(ctx, envID)
				if err != nil {
					writeInternalError(w, fmt.Errorf("workers: resolving environment %s for credential %s: %w", envID, cred.ID, err))
					return
				}
				envNames = append(envNames, env.Name)
			}

			item := workerCredentialResponse{
				WorkerCredentialID: cred.ID.String(),
				Name:               cred.Name,
				Environments:       envNames,
			}
			if cred.RevokedAt != nil {
				s := cred.RevokedAt.Format(time.RFC3339)
				item.RevokedAt = &s
			}
			resp = append(resp, item)
		}

		writeJSON(w, http.StatusOK, resp)
	})
}

// resolveWorkerEnvironments looks up each name in tenantID and checks it
// against claims' own scope, writing the response and returning ok=false on
// the first problem (unknown name, or outside claims' delegated scope).
func resolveWorkerEnvironments(ctx context.Context, w http.ResponseWriter, environments *environment.Store, claims *identity.Claims, tenantID uuid.UUID, names []string) (envIDs []uuid.UUID, ok bool) {
	envIDs = make([]uuid.UUID, 0, len(names))
	for _, name := range names {
		env, err := environments.GetByName(ctx, tenantID, name)
		if errors.Is(err, environment.ErrNotFound) {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("unknown environment %q", name))
			return nil, false
		}
		if err != nil {
			writeInternalError(w, err)
			return nil, false
		}
		// Same "cannot grant more than you have" rule as tier, applied to
		// environment scope. The root actor (no Delegation) is exempt.
		if claims.Delegation != nil && !claims.PermitsEnvironment(env.ID.String()) {
			writeError(w, http.StatusForbidden, codeForbidden, fmt.Sprintf("cannot grant environment %q outside your own scope", name))
			return nil, false
		}
		envIDs = append(envIDs, env.ID)
	}
	return envIDs, true
}

type bootstrapWorkerRequest struct {
	Name string `json:"name"`
}

// handlePostWorkerBootstrap self-registers a worker using a shared secret
// instead of a human session — see Decision 022. It can only ever produce a
// zero-environment credential; granting real access still requires an
// authenticated Autonomous-tier human via handlePostWorkerGrantEnvironments.
func handlePostWorkerBootstrap(bootstrapToken []byte, tenants *tenant.Store, actors *actor.Store, workers *workercred.Store, audits *audit.Store) http.Handler {
	const prefix = "Bearer "
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or malformed Authorization header")
			return
		}
		presented := []byte(strings.TrimPrefix(header, prefix))
		if subtle.ConstantTimeCompare(presented, bootstrapToken) != 1 {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "invalid bootstrap token")
			return
		}

		var req bootstrapWorkerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "name is required")
			return
		}

		ctx := r.Context()

		tn, err := tenants.GetSole(ctx)
		if errors.Is(err, tenant.ErrNotFound) {
			writeError(w, http.StatusConflict, codeConflict, "instance setup has not run yet")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		root, err := actors.GetRoot(ctx, tn.ID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("workers: bootstrap: loading root actor: %w", err))
			return
		}

		cred, token, err := workers.Bootstrap(ctx, tn.ID, req.Name)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: tn.ID,
			ActorID:  root.ID,
			Action:   "worker.self_registered",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, createWorkerResponse{WorkerCredentialID: cred.ID.String(), Token: token})
	})
}

type grantWorkerEnvironmentsRequest struct {
	Environments []string `json:"environments"`
}

// handlePostWorkerGrantEnvironments adds environments to an already-enrolled
// worker credential, so a running worker can gain access to a newly
// registered AWS account without being re-enrolled or redeployed — see
// Decision 020.
func handlePostWorkerGrantEnvironments(environments *environment.Store, workers *workercred.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("workers: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier != identity.TierAutonomous {
			writeError(w, http.StatusForbidden, codeForbidden, "granting a worker an environment requires the autonomous tier")
			return
		}

		credentialID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed worker credential id")
			return
		}

		var req grantWorkerEnvironmentsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
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

		cred, err := workers.Get(ctx, credentialID)
		if errors.Is(err, workercred.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown worker credential")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if cred.TenantID != tenantID {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown worker credential")
			return
		}

		envIDs, ok := resolveWorkerEnvironments(ctx, w, environments, claims, tenantID, req.Environments)
		if !ok {
			return
		}

		if err := workers.Grant(ctx, cred.ID, envIDs); err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: tenantID,
			ActorID:  actorID,
			Action:   "worker.environment_granted",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
