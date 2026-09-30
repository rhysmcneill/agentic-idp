package ciauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
	"github.com/rhysmcneill/agentic-idp/worker/internal/broker/aws"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
)

// pollInterval and pollTimeout bound the background status poll after a
// successful mint — a run that never reaches a terminal status within
// pollTimeout is reported timed_out rather than polled forever. var, not
// const, so tests can shrink them instead of waiting on real durations.
var (
	pollInterval = 30 * time.Second
	pollTimeout  = 6 * time.Hour
)

// resolveClient is the subset of controlplane.Client this package calls.
type resolveClient interface {
	ResolveRun(ctx context.Context, provider, repo, workflowRef, ref, ciExternalRef, ciURL string) (controlplane.ResolvedRun, error)
	ReportRunResult(ctx context.Context, runID string, result controlplane.RunResult) error
}

// statusAdapter is the subset of pkg/ci.Adapter the background poll calls.
type statusAdapter interface {
	Status(ctx context.Context, cfg ci.Config, h ci.Handle) (ci.RunStatus, error)
}

// githubCredential resolves a short-lived GitHub API token for one repo.
type githubCredential interface {
	InstallationToken(ctx context.Context, repo string) (ci.Secret, error)
}

// Handler answers a CI job's request for governed credentials: verify its
// token, resolve it to a run, mint, respond, then poll to a terminal status
// in the background.
type Handler struct {
	registry    *Registry
	cp          resolveClient
	broker      cloud.Broker
	adapter     statusAdapter
	githubCreds githubCredential
}

// NewHandler constructs the CI OIDC callback endpoint.
func NewHandler(registry *Registry, cp resolveClient, broker cloud.Broker, adapter statusAdapter, githubCreds githubCredential) http.Handler {
	return &Handler{registry: registry, cp: cp, broker: broker, adapter: adapter, githubCreds: githubCreds}
}

type callbackRequest struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
}

type credentialResponse struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token"`
	Expiration      time.Time `json:"expiration"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req callbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	verifier, err := h.registry.Get(ci.Provider(req.Provider))
	if err != nil {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}

	claims, err := verifier.Verify(ctx, req.Token)
	if err != nil {
		slog.Error("ciauth: verifying token", "error", err)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	resolved, err := h.cp.ResolveRun(ctx, req.Provider, claims.Repo, claims.WorkflowRef, claims.Ref, claims.RunID, claims.URL)
	if err != nil {
		slog.Error("ciauth: resolving run", "repo", claims.Repo, "workflow_ref", claims.WorkflowRef, "error", err)
		http.Error(w, "unable to resolve a matching run", http.StatusForbidden)
		return
	}

	tier := identity.Tier(resolved.Tier)
	cfg, err := aws.NewEnvironmentConfig(resolved.AccountRef, resolved.ExternalID, resolved.TrustAnchor, map[string]string{tier.String(): resolved.RoleARN})
	if err != nil {
		slog.Error("ciauth: building environment config", "run_id", resolved.RunID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	creds, err := h.broker.MintCredentials(ctx, cfg, tier)
	if err != nil {
		slog.Error("ciauth: minting credentials", "run_id", resolved.RunID, "error", err)
		http.Error(w, "minting credentials failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, credentialResponse{
		AccessKeyID:     string(creds.Values["access_key_id"]),
		SecretAccessKey: string(creds.Values["secret_access_key"]),
		SessionToken:    string(creds.Values["session_token"]),
		Expiration:      creds.ExpiresAt,
	})

	go h.pollUntilTerminal(claims.Repo, req.Provider, resolved.RunID, claims.RunID) // #nosec G118 -- deliberately detached: the poll must outlive this request, which is about to return
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// pollUntilTerminal polls the external run's status until it reaches a
// terminal state or pollTimeout elapses, then reports the result. Runs
// detached from the request that triggered it, so it uses its own context.
func (h *Handler) pollUntilTerminal(repo, provider, ourRunID, externalRunID string) {
	ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.reportResult(ourRunID, controlplane.RunResult{
				Status: string(ci.StatusTimedOut), CIRawStatus: "polling timed out before a terminal status was observed",
			})
			return
		case <-ticker.C:
		}

		status, err := h.checkStatus(ctx, repo, provider, externalRunID)
		if err != nil {
			slog.Error("ciauth: polling run status", "run_id", ourRunID, "error", err)
			continue
		}
		if !status.Status.Terminal() {
			continue
		}
		h.reportResult(ourRunID, controlplane.RunResult{
			Status: string(status.Status), CIExternalRef: externalRunID, CIURL: status.URL, CIRawStatus: status.Raw,
		})
		return
	}
}

func (h *Handler) checkStatus(ctx context.Context, repo, provider, externalRunID string) (ci.RunStatus, error) {
	token, err := h.githubCreds.InstallationToken(ctx, repo)
	if err != nil {
		return ci.RunStatus{}, fmt.Errorf("resolving credential: %w", err)
	}
	cfg := ci.Config{Provider: ci.Provider(provider), Settings: map[string]string{"repo": repo}, Credential: token}
	status, err := h.adapter.Status(ctx, cfg, ci.Handle{Resolved: true, ExternalID: externalRunID})
	if err != nil {
		return ci.RunStatus{}, fmt.Errorf("checking status: %w", err)
	}
	return status, nil
}

func (h *Handler) reportResult(runID string, result controlplane.RunResult) {
	if err := h.cp.ReportRunResult(context.Background(), runID, result); err != nil {
		slog.Error("ciauth: reporting run result", "run_id", runID, "error", err)
	}
}
