// Package githubactions implements pkg/ci.Adapter for GitHub Actions.
//
// It lives in pkg/, not a component's internal/, because both the worker
// (which actually calls Trigger/Status/Cancel) and the control plane (which
// only needs a concrete instance to satisfy its provider-registered check at
// controlplane/internal/execution.Worker.Work) must construct one, and the
// control plane cannot import worker-internal code.
package githubactions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
)

// SettingRepo is the Config.Settings key naming the target repository as
// "owner/name".
const SettingRepo = "repo"

const defaultBaseURL = "https://api.github.com"

// Adapter drives GitHub Actions over its REST API. Stateless and safe for
// concurrent use, per pkg/ci.Adapter's contract — configuration and
// credentials arrive per call via Config.
type Adapter struct {
	baseURL    string
	httpClient *http.Client
}

// New constructs an Adapter against the real GitHub API.
func New() *Adapter {
	return &Adapter{baseURL: defaultBaseURL, httpClient: http.DefaultClient}
}

// Provider implements ci.Adapter.
func (a *Adapter) Provider() ci.Provider { return ci.ProviderGitHubActions }

// Capabilities implements ci.Adapter. OIDCCallback is true: correlation and
// credential brokering happen via the CI OIDC callback (worker/internal/ciauth),
// not via Resolve, because GitHub's own Actions OIDC token carries the run's
// repository/workflow/run_id as verified claims — a stronger signal than
// anything Resolve could infer by listing and guessing.
func (a *Adapter) Capabilities() ci.Capabilities {
	return ci.Capabilities{
		TriggerReturnsID: false,
		Cancel:           true,
		Logs:             false,
		OIDCCallback:     true,
	}
}

// ValidateConfig implements ci.Adapter.
func (a *Adapter) ValidateConfig(_ context.Context, cfg ci.Config) error {
	if cfg.Provider != ci.ProviderGitHubActions {
		return fmt.Errorf("%w: githubactions cannot validate provider %q", ci.ErrInvalidConfig, cfg.Provider)
	}
	if err := cfg.Require(SettingRepo); err != nil {
		return fmt.Errorf("githubactions: %w", err)
	}
	return nil
}

// dispatchRequest is the body for POST .../workflows/{id}/dispatches.
type dispatchRequest struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs,omitempty"`
}

// Trigger implements ci.Adapter. GitHub's workflow_dispatch returns 204 with
// no run identifier, so the returned Handle is unresolved; the OIDC callback
// resolves it (see Capabilities).
func (a *Adapter) Trigger(ctx context.Context, cfg ci.Config, req ci.TriggerRequest) (ci.Handle, error) {
	if err := a.ValidateConfig(ctx, cfg); err != nil {
		return ci.Handle{}, err
	}

	body := dispatchRequest{Ref: req.Ref, Inputs: req.Inputs}
	path := fmt.Sprintf("/repos/%s/actions/workflows/%s/dispatches", cfg.Get(SettingRepo), req.Workflow)
	if err := a.do(ctx, cfg, http.MethodPost, path, body, nil); err != nil {
		return ci.Handle{}, fmt.Errorf("githubactions: trigger: %w", err)
	}

	return ci.Handle{
		Provider:    ci.ProviderGitHubActions,
		Resolved:    false,
		Correlation: req.Correlation,
	}, nil
}

// Resolve implements ci.Adapter. Not used for this provider: the OIDC
// callback resolves Handle.ExternalID from the run's verified run_id claim,
// which is authoritative rather than guessed from a listing.
func (a *Adapter) Resolve(_ context.Context, _ ci.Config, h ci.Handle) (ci.Handle, error) {
	return h, ci.ErrUnsupported
}

// runResponse is the subset of GitHub's workflow run object this adapter reads.
type runResponse struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	RunStarted string `json:"run_started_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Status implements ci.Adapter.
func (a *Adapter) Status(ctx context.Context, cfg ci.Config, h ci.Handle) (ci.RunStatus, error) {
	if !h.Resolved || h.ExternalID == "" {
		return ci.RunStatus{}, ci.ErrNotResolved
	}

	var run runResponse
	path := fmt.Sprintf("/repos/%s/actions/runs/%s", cfg.Get(SettingRepo), h.ExternalID)
	if err := a.do(ctx, cfg, http.MethodGet, path, nil, &run); err != nil {
		return ci.RunStatus{}, fmt.Errorf("githubactions: status: %w", err)
	}

	out := ci.RunStatus{
		Status: mapStatus(run.Status, run.Conclusion),
		Raw:    run.Conclusion,
		URL:    run.HTMLURL,
	}
	if run.Conclusion == "" {
		out.Raw = run.Status
	}
	if t, err := time.Parse(time.RFC3339, run.RunStarted); err == nil {
		out.StartedAt = &t
	}
	if out.Status.Terminal() {
		if t, err := time.Parse(time.RFC3339, run.UpdatedAt); err == nil {
			out.FinishedAt = &t
		}
	}
	return out, nil
}

// mapStatus normalises GitHub's split status/conclusion pair into a single
// ci.Status. GitHub reports completion state across two fields; conclusions
// outside the common set (action_required, neutral, skipped, stale) are
// treated as failures since none of them represent a successful run.
func mapStatus(status, conclusion string) ci.Status {
	if status != "completed" {
		switch status {
		case "queued", "waiting", "requested", "pending":
			return ci.StatusPending
		case "in_progress":
			return ci.StatusRunning
		default:
			return ci.StatusUnknown
		}
	}
	switch conclusion {
	case "success":
		return ci.StatusSucceeded
	case "cancelled":
		return ci.StatusCancelled
	case "timed_out":
		return ci.StatusTimedOut
	default:
		return ci.StatusFailed
	}
}

// Logs implements ci.Adapter. Unsupported for v1 — see Capabilities.
func (a *Adapter) Logs(_ context.Context, _ ci.Config, _ ci.Handle, _ string) (ci.LogChunk, error) {
	return ci.LogChunk{}, ci.ErrUnsupported
}

// Cancel implements ci.Adapter.
func (a *Adapter) Cancel(ctx context.Context, cfg ci.Config, h ci.Handle) error {
	if !h.Resolved || h.ExternalID == "" {
		return ci.ErrNotResolved
	}
	path := fmt.Sprintf("/repos/%s/actions/runs/%s/cancel", cfg.Get(SettingRepo), h.ExternalID)
	if err := a.do(ctx, cfg, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("githubactions: cancel: %w", err)
	}
	return nil
}

// githubError is the shape of GitHub's REST API error responses.
type githubError struct {
	Message string `json:"message"`
}

// do issues an authenticated request against the GitHub REST API, decoding
// the JSON response into out (when non-nil) or GitHub's error body into the
// returned error.
func (a *Adapter) do(ctx context.Context, cfg ci.Config, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshalling request body: %w", err)
		}
		reqBody = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Credential.Reveal())
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		var ghErr githubError
		_ = json.NewDecoder(resp.Body).Decode(&ghErr)
		if ghErr.Message == "" {
			ghErr.Message = resp.Status
		}
		return fmt.Errorf("github api: %s: %s", resp.Status, ghErr.Message)
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}
