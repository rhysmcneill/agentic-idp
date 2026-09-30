// Package controlplane is the worker's HTTP client for the control plane's
// worker-facing API. It authenticates with a worker credential (an opaque
// bearer token minted by idpctl worker enrol), never an actor token — the
// control plane checks it with its own hash lookup, entirely separate from
// pkg/identity's Verifier.
package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Job is a claimed verification: everything the worker needs to run the
// connectivity check for one environment. RoleARNs is keyed by tier name
// (read_only, human_in_the_loop, autonomous).
type Job struct {
	VerificationID string
	Environment    string
	Provider       string
	AccountRef     string
	ExternalID     string
	TrustAnchor    string
	RoleARNs       map[string]string
}

// TierResult is the outcome of assuming one tier's role.
type TierResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Client talks to one control plane instance's worker-facing endpoints.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Client authenticating as token against baseURL.
func New(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{}}
}

// Bootstrap self-registers name against baseURL using the control plane's
// shared bootstrap secret (Decision 022) and returns the worker credential's
// ID (safe to log — an operator needs it to grant environments later) and
// token to use for the ongoing poll loop. Get-or-rotate, so it's safe to
// call on every startup instead of caching a token across restarts.
func Bootstrap(ctx context.Context, baseURL, bootstrapToken, name string) (credentialID, token string, err error) {
	body, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: name})
	if err != nil {
		return "", "", fmt.Errorf("controlplane: marshalling bootstrap request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/workers/bootstrap", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("controlplane: building bootstrap request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bootstrapToken)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("controlplane: bootstrapping: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("controlplane: bootstrapping: unexpected status %d", resp.StatusCode)
	}

	var wire struct {
		WorkerCredentialID string `json:"worker_credential_id"`
		Token              string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return "", "", fmt.Errorf("controlplane: decoding bootstrap response: %w", err)
	}
	return wire.WorkerCredentialID, wire.Token, nil
}

// NextJob claims the oldest pending verification scoped to this worker
// credential's granted environments, or returns a nil Job if nothing is
// pending.
func (c *Client) NextJob(ctx context.Context) (*Job, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/worker/verifications/next", nil)
	if err != nil {
		return nil, fmt.Errorf("controlplane: building next-job request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("controlplane: fetching next job: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("controlplane: fetching next job: unexpected status %d", resp.StatusCode)
	}

	var wire struct {
		VerificationID string            `json:"verification_id"`
		Environment    string            `json:"environment"`
		Provider       string            `json:"provider"`
		AccountRef     string            `json:"account_ref"`
		ExternalID     string            `json:"external_id"`
		TrustAnchor    string            `json:"trust_anchor"`
		RoleARNs       map[string]string `json:"role_arns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("controlplane: decoding next job: %w", err)
	}

	return &Job{
		VerificationID: wire.VerificationID,
		Environment:    wire.Environment,
		Provider:       wire.Provider,
		AccountRef:     wire.AccountRef,
		ExternalID:     wire.ExternalID,
		TrustAnchor:    wire.TrustAnchor,
		RoleARNs:       wire.RoleARNs,
	}, nil
}

// Run is a claimed run: everything the worker needs to dispatch its
// pipeline.
type Run struct {
	RunID       string
	Provider    string
	WorkflowRef string
	Settings    map[string]string
}

// NextRun claims the oldest un-claimed executing run scoped to this worker
// credential's granted environments, or returns a nil Run if none is
// executing.
func (c *Client) NextRun(ctx context.Context) (*Run, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/worker/runs/next", nil)
	if err != nil {
		return nil, fmt.Errorf("controlplane: building next-run request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("controlplane: fetching next run: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("controlplane: fetching next run: unexpected status %d", resp.StatusCode)
	}

	var wire struct {
		RunID       string            `json:"run_id"`
		Provider    string            `json:"provider"`
		WorkflowRef string            `json:"workflow_ref"`
		Settings    map[string]string `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return nil, fmt.Errorf("controlplane: decoding next run: %w", err)
	}

	return &Run{
		RunID:       wire.RunID,
		Provider:    wire.Provider,
		WorkflowRef: wire.WorkflowRef,
		Settings:    wire.Settings,
	}, nil
}

// RunResult is the terminal outcome of a claimed run.
type RunResult struct {
	Status        string
	CIExternalRef string
	CIURL         string
	CIRawStatus   string
}

// ReportRunResult posts the outcome of a claimed run back to the control
// plane.
func (c *Client) ReportRunResult(ctx context.Context, runID string, result RunResult) error {
	body, err := json.Marshal(struct {
		Status        string `json:"status"`
		CIExternalRef string `json:"ci_external_ref"`
		CIURL         string `json:"ci_url"`
		CIRawStatus   string `json:"ci_raw_status"`
	}{
		Status:        result.Status,
		CIExternalRef: result.CIExternalRef,
		CIURL:         result.CIURL,
		CIRawStatus:   result.CIRawStatus,
	})
	if err != nil {
		return fmt.Errorf("controlplane: marshalling run result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/worker/runs/"+runID+"/result", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("controlplane: building report-run-result request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("controlplane: reporting run result: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("controlplane: reporting run result: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ResolvedRun is what the control plane returns once a CI job's claims are
// matched to a run: everything needed to mint tier-scoped AWS credentials.
type ResolvedRun struct {
	RunID       string
	Tier        int16
	AccountRef  string
	ExternalID  string
	TrustAnchor string
	RoleARN     string
}

// ResolveRun matches provider/repo/workflowRef/ref to the run they
// authorise, records ciExternalRef/ciURL against it, and returns what's
// needed to mint credentials.
func (c *Client) ResolveRun(ctx context.Context, provider, repo, workflowRef, ref, ciExternalRef, ciURL string) (ResolvedRun, error) {
	body, err := json.Marshal(struct {
		Provider      string `json:"provider"`
		Repo          string `json:"repo"`
		WorkflowRef   string `json:"workflow_ref"`
		Ref           string `json:"ref"`
		CIExternalRef string `json:"ci_external_ref"`
		CIURL         string `json:"ci_url"`
	}{
		Provider: provider, Repo: repo, WorkflowRef: workflowRef, Ref: ref,
		CIExternalRef: ciExternalRef, CIURL: ciURL,
	})
	if err != nil {
		return ResolvedRun{}, fmt.Errorf("controlplane: marshalling resolve request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/worker/runs/resolve", bytes.NewReader(body))
	if err != nil {
		return ResolvedRun{}, fmt.Errorf("controlplane: building resolve request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return ResolvedRun{}, fmt.Errorf("controlplane: resolving run: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ResolvedRun{}, fmt.Errorf("controlplane: resolving run: unexpected status %d", resp.StatusCode)
	}

	var wire struct {
		RunID       string `json:"run_id"`
		Tier        int16  `json:"tier"`
		AccountRef  string `json:"account_ref"`
		ExternalID  string `json:"external_id"`
		TrustAnchor string `json:"trust_anchor"`
		RoleARN     string `json:"role_arn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return ResolvedRun{}, fmt.Errorf("controlplane: decoding resolve response: %w", err)
	}

	return ResolvedRun{
		RunID: wire.RunID, Tier: wire.Tier, AccountRef: wire.AccountRef,
		ExternalID: wire.ExternalID, TrustAnchor: wire.TrustAnchor, RoleARN: wire.RoleARN,
	}, nil
}

// RegisterCICallback tells the control plane this worker's own CI OIDC
// callback is reachable at url, so idpctl ci auth can discover it.
func (c *Client) RegisterCICallback(ctx context.Context, url string) error {
	body, err := json.Marshal(struct {
		URL string `json:"url"`
	}{URL: url})
	if err != nil {
		return fmt.Errorf("controlplane: marshalling callback registration: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/v1/worker/ci-callback-url", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("controlplane: building callback registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("controlplane: registering ci callback: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("controlplane: registering ci callback: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ReportResult posts the outcome of a claimed verification back to the
// control plane.
func (c *Client) ReportResult(ctx context.Context, verificationID string, results map[string]TierResult) error {
	body, err := json.Marshal(struct {
		TierResults map[string]TierResult `json:"tier_results"`
	}{TierResults: results})
	if err != nil {
		return fmt.Errorf("controlplane: marshalling result: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/worker/verifications/"+verificationID+"/result", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("controlplane: building report-result request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("controlplane: reporting result: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("controlplane: reporting result: unexpected status %d", resp.StatusCode)
	}
	return nil
}
