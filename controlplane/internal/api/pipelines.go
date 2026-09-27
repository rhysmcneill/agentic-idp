package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// pipelineProviders is the set of ci.Provider values a caller may name.
var pipelineProviders = map[string]ci.Provider{
	"github_actions":      ci.ProviderGitHubActions,
	"gitlab_ci":           ci.ProviderGitLabCI,
	"jenkins":             ci.ProviderJenkins,
	"atlantis":            ci.ProviderAtlantis,
	"bitbucket_pipelines": ci.ProviderBitbucket,
}

type createPipelineRequest struct {
	EnvironmentID string            `json:"environment_id"`
	Provider      string            `json:"provider"`
	WorkflowRef   string            `json:"workflow_ref"`
	Settings      map[string]string `json:"settings"`
	Mutating      *bool             `json:"mutating"`
}

type pipelineResponse struct {
	PipelineID    string            `json:"pipeline_id"`
	EnvironmentID string            `json:"environment_id"`
	Provider      string            `json:"provider"`
	WorkflowRef   string            `json:"workflow_ref"`
	Settings      map[string]string `json:"settings"`
	Mutating      bool              `json:"mutating"`
}

// handlePostPipelines registers a Pipeline: which CI provider and workflow a
// run targets, and whether it can mutate its Environment. Mutating defaults
// to true (the safe default — an unmarked pipeline is treated as capable of
// changing the environment, never assumed safe for ReadOnly to trigger).
func handlePostPipelines(environments *environment.Store, pipelines *pipeline.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("pipelines: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier == identity.TierReadOnly {
			writeError(w, http.StatusForbidden, codeForbidden, "registering a pipeline requires human_in_the_loop or autonomous tier")
			return
		}

		var req createPipelineRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		environmentID, err := uuid.Parse(req.EnvironmentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "environment_id must be a valid UUID")
			return
		}
		provider, ok := pipelineProviders[req.Provider]
		if !ok {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("unknown provider %q", req.Provider))
			return
		}
		if req.WorkflowRef == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "workflow_ref is required")
			return
		}
		// Declaring a pipeline safe for ReadOnly's unattended execution is
		// itself an unsupervised claim — only Autonomous, which already acts
		// unsupervised, may vouch for it; a HumanInTheLoop actor's pipelines
		// are always treated as mutating regardless of what it requests.
		mutating := true
		if req.Mutating != nil && claims.Tier == identity.TierAutonomous {
			mutating = *req.Mutating
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("pipelines: parsing tenant id from claims: %w", err))
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

		p, err := pipelines.Create(ctx, tenantID, environmentID, provider, req.WorkflowRef, req.Settings, mutating)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, pipelineToResponse(p))
	})
}

// handleGetPipeline returns a single Pipeline, tenant-scoped.
func handleGetPipeline(pipelines *pipeline.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("pipelines: no claims in request context — requireAuth was not applied"))
			return
		}
		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("pipelines: parsing tenant id from claims: %w", err))
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "id must be a valid UUID")
			return
		}

		p, err := pipelines.Get(r.Context(), tenantID, id)
		if errors.Is(err, pipeline.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown pipeline")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, pipelineToResponse(p))
	})
}

func pipelineToResponse(p pipeline.Pipeline) pipelineResponse {
	return pipelineResponse{
		PipelineID:    p.ID.String(),
		EnvironmentID: p.EnvironmentID.String(),
		Provider:      string(p.Provider),
		WorkflowRef:   p.WorkflowRef,
		Settings:      p.Settings,
		Mutating:      p.Mutating,
	}
}
