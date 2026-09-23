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

// SetupRequest is POST /v1/setup's request body.
type SetupRequest struct {
	TenantName    string `json:"tenant_name"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
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
