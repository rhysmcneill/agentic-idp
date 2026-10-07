// Package client is idpctl's HTTP client for the control plane API. It never
// holds cloud credentials — only a short-lived session token, same as any
// other actor.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// APIError is returned when the control plane responds with a structured
// {code, message} error body.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s (%s, status %d)", e.Message, e.Code, e.StatusCode)
}

// Client talks to one control plane instance.
type Client struct {
	baseURL string
	http    *http.Client
}

// New constructs a Client against baseURL (e.g. "http://localhost:8080").
func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{}}
}

// NeedsSetupResponse is GET /v1/setup's response body.
type NeedsSetupResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

// NeedsSetup reports whether the instance still needs first-run setup.
func (c *Client) NeedsSetup(ctx context.Context) (bool, error) {
	var resp NeedsSetupResponse
	if err := c.do(ctx, http.MethodGet, "/v1/setup", "", nil, &resp); err != nil {
		return false, err
	}
	return resp.NeedsSetup, nil
}

// GetCICallbackURL discovers the CI OIDC callback URL registered by a
// worker granted an environment with a provider+repo pipeline — scoped this
// way so the answer can't be hijacked by a worker unrelated to this repo.
// Unauthenticated, since the value isn't sensitive and a CI job calling this
// has no session token of its own yet.
func (c *Client) GetCICallbackURL(ctx context.Context, provider, repo string) (string, error) {
	path := "/v1/ci/callback-url?provider=" + url.QueryEscape(provider) + "&repo=" + url.QueryEscape(repo)
	var resp struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodGet, path, "", nil, &resp); err != nil {
		return "", err
	}
	return resp.URL, nil
}

// SetupRequest is POST /v1/setup's request body.
type SetupRequest struct {
	TenantName       string `json:"tenant_name"`
	AdminUsername    string `json:"admin_username"`
	AdminPassword    string `json:"admin_password"`
	TelemetryEnabled bool   `json:"telemetry_enabled"`
}

// SetupResponse is POST /v1/setup's response body.
type SetupResponse struct {
	TenantID string `json:"tenant_id"`
	ActorID  string `json:"actor_id"`
	Token    string `json:"token"`
}

// Setup runs first-run setup. Only ever succeeds once per instance.
func (c *Client) Setup(ctx context.Context, req SetupRequest) (SetupResponse, error) {
	var resp SetupResponse
	if err := c.do(ctx, http.MethodPost, "/v1/setup", "", req, &resp); err != nil {
		return SetupResponse{}, err
	}
	return resp, nil
}

// TokenRequest is POST /v1/token's request body.
type TokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// TokenResponse is POST /v1/token's response body.
type TokenResponse struct {
	Token string `json:"token"`
}

// Token exchanges a local username and password for a session token.
func (c *Client) Token(ctx context.Context, req TokenRequest) (TokenResponse, error) {
	var resp TokenResponse
	if err := c.do(ctx, http.MethodPost, "/v1/token", "", req, &resp); err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// CreateAgentRequest is POST /v1/agents' request body.
type CreateAgentRequest struct {
	Name         string   `json:"name"`
	Team         string   `json:"team"`
	Tier         string   `json:"tier"`
	Environments []string `json:"environments,omitempty"`
	TTLSeconds   int64    `json:"ttl_seconds"`

	// IdempotencyKey, when set, makes a retried call return the agent an
	// earlier call already created instead of a duplicate.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// CreateAgentResponse is POST /v1/agents' response body.
type CreateAgentResponse struct {
	ActorID string `json:"actor_id"`
	Token   string `json:"token"`
}

// CreateAgent enrols a new agent, authenticated as token.
func (c *Client) CreateAgent(ctx context.Context, token string, req CreateAgentRequest) (CreateAgentResponse, error) {
	var resp CreateAgentResponse
	if err := c.do(ctx, http.MethodPost, "/v1/agents", token, req, &resp); err != nil {
		return CreateAgentResponse{}, err
	}
	return resp, nil
}

// CreateEnvironmentRequest is POST /v1/environments' request body. RoleARNs
// is keyed by tier name ("read_only", "human_in_the_loop", "autonomous") and
// must supply all three.
type CreateEnvironmentRequest struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	Region      string            `json:"region"`
	AccountRef  string            `json:"account_ref"`
	ExternalID  string            `json:"external_id"`
	TrustAnchor string            `json:"trust_anchor"`
	RoleARNs    map[string]string `json:"role_arns"`
}

// CreateEnvironmentResponse is POST /v1/environments' response body. It never
// echoes external_id or trust_anchor back: once registered, a value that
// weakens the confused-deputy protection if leaked shouldn't also be sitting
// in every client's response logs.
type CreateEnvironmentResponse struct {
	EnvironmentID string `json:"environment_id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Region        string `json:"region"`
}

// CreateEnvironment registers a new environment, authenticated as token.
func (c *Client) CreateEnvironment(ctx context.Context, token string, req CreateEnvironmentRequest) (CreateEnvironmentResponse, error) {
	var resp CreateEnvironmentResponse
	if err := c.do(ctx, http.MethodPost, "/v1/environments", token, req, &resp); err != nil {
		return CreateEnvironmentResponse{}, err
	}
	return resp, nil
}

// CreateWorkerRequest is POST /v1/workers' request body.
type CreateWorkerRequest struct {
	Name         string   `json:"name"`
	Environments []string `json:"environments"`
}

// CreateWorkerResponse is POST /v1/workers' response body. The token is
// shown to the operator exactly once here — it is never recoverable from the
// control plane afterward.
type CreateWorkerResponse struct {
	WorkerCredentialID string `json:"worker_credential_id"`
	Token              string `json:"token"`
}

// CreateWorker enrols a new worker credential, authenticated as token.
func (c *Client) CreateWorker(ctx context.Context, token string, req CreateWorkerRequest) (CreateWorkerResponse, error) {
	var resp CreateWorkerResponse
	if err := c.do(ctx, http.MethodPost, "/v1/workers", token, req, &resp); err != nil {
		return CreateWorkerResponse{}, err
	}
	return resp, nil
}

// WorkerCredential is one entry in GET /v1/workers' response body.
type WorkerCredential struct {
	WorkerCredentialID string   `json:"worker_credential_id"`
	Name               string   `json:"name"`
	Environments       []string `json:"environments"`
	RevokedAt          *string  `json:"revoked_at,omitempty"`
}

// ListWorkers returns every worker credential in the caller's tenant — how
// an operator finds a credential's ID by the name they gave it.
func (c *Client) ListWorkers(ctx context.Context, token string) ([]WorkerCredential, error) {
	var resp []WorkerCredential
	if err := c.do(ctx, http.MethodGet, "/v1/workers", token, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// GrantWorkerEnvironmentsRequest is POST /v1/workers/{id}/environments'
// request body.
type GrantWorkerEnvironmentsRequest struct {
	Environments []string `json:"environments"`
}

// GrantWorkerEnvironments adds environments to an already-enrolled worker
// credential — see Decision 020, the way a running worker gains access to a
// newly onboarded AWS account without being re-enrolled or redeployed.
func (c *Client) GrantWorkerEnvironments(ctx context.Context, token, workerCredentialID string, req GrantWorkerEnvironmentsRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/workers/"+workerCredentialID+"/environments", token, req, nil)
}

// TriggerVerificationResponse is POST /v1/environments/{name}/verify's
// response body.
type TriggerVerificationResponse struct {
	VerificationID string `json:"verification_id"`
	Status         string `json:"status"`
}

// TriggerVerification starts a connectivity check against the named
// environment, authenticated as token. A worker claims and executes it
// asynchronously; poll GetVerification for the result.
func (c *Client) TriggerVerification(ctx context.Context, token, environmentName string) (TriggerVerificationResponse, error) {
	var resp TriggerVerificationResponse
	if err := c.do(ctx, http.MethodPost, "/v1/environments/"+environmentName+"/verify", token, nil, &resp); err != nil {
		return TriggerVerificationResponse{}, err
	}
	return resp, nil
}

// TierResult is the outcome of assuming one tier's role.
type TierResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// GetVerificationResponse is GET
// /v1/environments/{name}/verifications/{id}'s response body.
type GetVerificationResponse struct {
	VerificationID string                `json:"verification_id"`
	Status         string                `json:"status"`
	TierResults    map[string]TierResult `json:"tier_results,omitempty"`
}

// GetVerification returns the current status of a triggered verification,
// authenticated as token.
func (c *Client) GetVerification(ctx context.Context, token, environmentName, verificationID string) (GetVerificationResponse, error) {
	var resp GetVerificationResponse
	if err := c.do(ctx, http.MethodGet, "/v1/environments/"+environmentName+"/verifications/"+verificationID, token, nil, &resp); err != nil {
		return GetVerificationResponse{}, err
	}
	return resp, nil
}

// setTelemetryRequest is PATCH /v1/tenants/telemetry's request body.
type setTelemetryRequest struct {
	Enabled bool `json:"enabled"`
}

// SetTelemetry toggles the caller's tenant's opt-in telemetry flag,
// authenticated as token.
func (c *Client) SetTelemetry(ctx context.Context, token string, enabled bool) error {
	return c.do(ctx, http.MethodPatch, "/v1/tenants/telemetry", token, setTelemetryRequest{Enabled: enabled}, nil)
}

// RunCost is one entry in GET /v1/runs/{id}/cost's response body.
type RunCost struct {
	Source       string `json:"source"`
	DurationMS   *int64 `json:"duration_ms,omitempty"`
	AmountMicros *int64 `json:"amount_micros,omitempty"`
	Currency     string `json:"currency"`
	CapturedAt   string `json:"captured_at"`
}

// GetRunCost returns every cost row captured against a run, authenticated as token.
func (c *Client) GetRunCost(ctx context.Context, token, runID string) ([]RunCost, error) {
	var resp []RunCost
	if err := c.do(ctx, http.MethodGet, "/v1/runs/"+runID+"/cost", token, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ActorCostSummary is one entry in GET /v1/costs/by-actor's response body.
type ActorCostSummary struct {
	ActorID           string `json:"actor_id"`
	TotalAmountMicros *int64 `json:"total_amount_micros,omitempty"`
	TotalDurationMS   int64  `json:"total_duration_ms"`
	RunCount          int64  `json:"run_count"`
}

// GetCostsByActorParams narrows the by-actor rollup. Zero values mean
// unfiltered.
type GetCostsByActorParams struct {
	ActorID string
	From    string // RFC3339, or empty
	To      string // RFC3339, or empty
}

// GetCostsByActor returns the cost-per-actor rollup, authenticated as token.
func (c *Client) GetCostsByActor(ctx context.Context, token string, params GetCostsByActorParams) ([]ActorCostSummary, error) {
	q := url.Values{}
	if params.ActorID != "" {
		q.Set("actor_id", params.ActorID)
	}
	if params.From != "" {
		q.Set("from", params.From)
	}
	if params.To != "" {
		q.Set("to", params.To)
	}
	path := "/v1/costs/by-actor"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var resp []ActorCostSummary
	if err := c.do(ctx, http.MethodGet, path, token, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// SetCostRateRequest is PUT /v1/cost-rates' request body.
type SetCostRateRequest struct {
	EnvironmentID   string `json:"environment_id,omitempty"`
	CIProvider      string `json:"ci_provider,omitempty"`
	RateMicrosPerMS int64  `json:"rate_micros_per_ms"`
	Currency        string `json:"currency,omitempty"`
}

// SetCostRate sets the operator-declared CI-duration-to-cost rate for a
// scope, authenticated as token.
func (c *Client) SetCostRate(ctx context.Context, token string, req SetCostRateRequest) error {
	return c.do(ctx, http.MethodPut, "/v1/cost-rates", token, req, nil)
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: marshalling request body: %w", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("client: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&apiErr); err != nil {
			return fmt.Errorf("client: %s %s: unexpected status %d", method, path, resp.StatusCode)
		}
		return &APIError{StatusCode: resp.StatusCode, Code: apiErr.Code, Message: apiErr.Message}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("client: %s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// CreatePipelineRequest is POST /v1/pipelines' request body. Mutating is a
// pointer so an unset flag lets the control plane apply its own default
// (true) rather than the CLI silently sending false.
type CreatePipelineRequest struct {
	EnvironmentID string            `json:"environment_id"`
	Provider      string            `json:"provider"`
	WorkflowRef   string            `json:"workflow_ref"`
	Settings      map[string]string `json:"settings,omitempty"`
	Mutating      *bool             `json:"mutating,omitempty"`
}

// PipelineResponse is both POST /v1/pipelines' and GET /v1/pipelines/{id}'s
// response body.
type PipelineResponse struct {
	PipelineID    string            `json:"pipeline_id"`
	EnvironmentID string            `json:"environment_id"`
	Provider      string            `json:"provider"`
	WorkflowRef   string            `json:"workflow_ref"`
	Settings      map[string]string `json:"settings,omitempty"`
	Mutating      bool              `json:"mutating"`
}

// CreatePipeline registers a new pipeline, authenticated as token.
func (c *Client) CreatePipeline(ctx context.Context, token string, req CreatePipelineRequest) (PipelineResponse, error) {
	var resp PipelineResponse
	if err := c.do(ctx, http.MethodPost, "/v1/pipelines", token, req, &resp); err != nil {
		return PipelineResponse{}, err
	}
	return resp, nil
}

// GetPipeline returns a single pipeline by ID, authenticated as token.
func (c *Client) GetPipeline(ctx context.Context, token, pipelineID string) (PipelineResponse, error) {
	var resp PipelineResponse
	if err := c.do(ctx, http.MethodGet, "/v1/pipelines/"+pipelineID, token, nil, &resp); err != nil {
		return PipelineResponse{}, err
	}
	return resp, nil
}

// CreateRunRequest is POST /v1/runs' request body. Tier is never a field
// here — it comes from the caller's verified token claims, not the request.
type CreateRunRequest struct {
	EnvironmentID  string `json:"environment_id"`
	PipelineID     string `json:"pipeline_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	DiffRef        string `json:"diff_ref,omitempty"`
}

// RunResponse is POST /v1/runs', GET /v1/runs/{id}'s and
// POST /v1/runs/{id}/decision's response body.
type RunResponse struct {
	RunID         string `json:"run_id"`
	EnvironmentID string `json:"environment_id"`
	PipelineID    string `json:"pipeline_id"`
	Tier          string `json:"tier"`
	Status        string `json:"status"`
}

// RequestRun creates a new run, authenticated as token. Returns immediately
// per the async run model — poll GetRun for its status.
func (c *Client) RequestRun(ctx context.Context, token string, req CreateRunRequest) (RunResponse, error) {
	var resp RunResponse
	if err := c.do(ctx, http.MethodPost, "/v1/runs", token, req, &resp); err != nil {
		return RunResponse{}, err
	}
	return resp, nil
}

// GetRun returns a single run by ID, authenticated as token.
func (c *Client) GetRun(ctx context.Context, token, runID string) (RunResponse, error) {
	var resp RunResponse
	if err := c.do(ctx, http.MethodGet, "/v1/runs/"+runID, token, nil, &resp); err != nil {
		return RunResponse{}, err
	}
	return resp, nil
}

// decideRunRequest is POST /v1/runs/{id}/decision's request body.
type decideRunRequest struct {
	Decision string `json:"decision"`
}

// DecideRun records an approve/deny decision on a run awaiting approval,
// authenticated as token.
func (c *Client) DecideRun(ctx context.Context, token, runID string, approved bool) (RunResponse, error) {
	decision := "denied"
	if approved {
		decision = "approved"
	}
	var resp RunResponse
	if err := c.do(ctx, http.MethodPost, "/v1/runs/"+runID+"/decision", token, decideRunRequest{Decision: decision}, &resp); err != nil {
		return RunResponse{}, err
	}
	return resp, nil
}
