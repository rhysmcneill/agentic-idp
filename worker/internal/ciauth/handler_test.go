package ciauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
)

// fakeVerifier lets tests control Verify's outcome without a real token.
type fakeVerifier struct {
	provider ci.Provider
	claims   Claims
	failWith error
}

func (f *fakeVerifier) Provider() ci.Provider { return f.provider }

func (f *fakeVerifier) Verify(context.Context, string) (Claims, error) {
	if f.failWith != nil {
		return Claims{}, f.failWith
	}
	return f.claims, nil
}

// fakeResolveClient lets tests control ResolveRun/ReportRunResult without a
// real control plane.
type fakeResolveClient struct {
	mu             sync.Mutex
	resolved       controlplane.ResolvedRun
	resolveFailErr error
	reportResults  []controlplane.RunResult
	reportDone     chan struct{}
}

func (f *fakeResolveClient) ResolveRun(context.Context, string, string, string, string, string, string) (controlplane.ResolvedRun, error) {
	if f.resolveFailErr != nil {
		return controlplane.ResolvedRun{}, f.resolveFailErr
	}
	return f.resolved, nil
}

func (f *fakeResolveClient) ReportRunResult(_ context.Context, _ string, result controlplane.RunResult) error {
	f.mu.Lock()
	f.reportResults = append(f.reportResults, result)
	f.mu.Unlock()
	if f.reportDone != nil {
		close(f.reportDone)
	}
	return nil
}

func (f *fakeResolveClient) lastResult() controlplane.RunResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reportResults[len(f.reportResults)-1]
}

// fakeBroker lets tests control MintCredentials without calling real AWS.
type fakeBroker struct {
	failWith error
}

func (f *fakeBroker) Provider() cloud.Provider { return cloud.ProviderAWS }
func (f *fakeBroker) ValidateEnvironment(context.Context, cloud.EnvironmentConfig) error {
	return nil
}
func (f *fakeBroker) MintCredentials(context.Context, cloud.EnvironmentConfig, identity.Tier) (cloud.Credentials, error) {
	if f.failWith != nil {
		return cloud.Credentials{}, f.failWith
	}
	return cloud.Credentials{
		Provider: cloud.ProviderAWS,
		Values: map[string]cloud.Secret{
			"access_key_id": "AKIATEST", "secret_access_key": "secret", "session_token": "token",
		},
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}, nil
}

// fakeStatusAdapter lets tests control Status's outcome and observe how many
// times it's polled before returning a terminal status.
type fakeStatusAdapter struct {
	mu       sync.Mutex
	statuses []ci.RunStatus
	calls    int
}

func (f *fakeStatusAdapter) Status(context.Context, ci.Config, ci.Handle) (ci.RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	f.calls++
	return f.statuses[i], nil
}

type fakeGitHubCredential struct{}

func (fakeGitHubCredential) InstallationToken(context.Context, string) (ci.Secret, error) {
	return "fake-token", nil
}

func resolvedRun() controlplane.ResolvedRun {
	return controlplane.ResolvedRun{
		RunID: "run-1", Tier: int16(identity.TierAutonomous),
		AccountRef: "123456789012", ExternalID: "ext-id", TrustAnchor: "arn:aws:iam::123456789012:role/worker",
		RoleARN: "arn:aws:iam::123456789012:role/tier3",
	}
}

func TestHandler_Success(t *testing.T) {
	cp := &fakeResolveClient{resolved: resolvedRun()}
	registry := NewRegistry(&fakeVerifier{provider: ci.ProviderGitHubActions, claims: Claims{Repo: "acme/widgets", RunID: "42"}})
	h := NewHandler(registry, cp, &fakeBroker{}, &fakeStatusAdapter{statuses: []ci.RunStatus{{Status: ci.StatusSucceeded}}}, fakeGitHubCredential{})

	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"provider":"github_actions","token":"whatever"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var creds credentialResponse
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if creds.AccessKeyID != "AKIATEST" {
		t.Errorf("AccessKeyID = %q, want %q", creds.AccessKeyID, "AKIATEST")
	}
}

func TestHandler_UnknownProvider(t *testing.T) {
	registry := NewRegistry()
	h := NewHandler(registry, &fakeResolveClient{}, &fakeBroker{}, &fakeStatusAdapter{}, fakeGitHubCredential{})

	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"provider":"github_actions","token":"whatever"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestHandler_VerifyFails(t *testing.T) {
	registry := NewRegistry(&fakeVerifier{provider: ci.ProviderGitHubActions, failWith: errors.New("fake: bad signature")})
	h := NewHandler(registry, &fakeResolveClient{}, &fakeBroker{}, &fakeStatusAdapter{}, fakeGitHubCredential{})

	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"provider":"github_actions","token":"whatever"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestHandler_ResolveFails(t *testing.T) {
	cp := &fakeResolveClient{resolveFailErr: errors.New("fake: no matching run")}
	registry := NewRegistry(&fakeVerifier{provider: ci.ProviderGitHubActions, claims: Claims{Repo: "acme/widgets"}})
	h := NewHandler(registry, cp, &fakeBroker{}, &fakeStatusAdapter{}, fakeGitHubCredential{})

	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"provider":"github_actions","token":"whatever"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestHandler_MintFails(t *testing.T) {
	cp := &fakeResolveClient{resolved: resolvedRun()}
	registry := NewRegistry(&fakeVerifier{provider: ci.ProviderGitHubActions, claims: Claims{Repo: "acme/widgets"}})
	h := NewHandler(registry, cp, &fakeBroker{failWith: errors.New("fake: AccessDenied")}, &fakeStatusAdapter{}, fakeGitHubCredential{})

	server := httptest.NewServer(h)
	defer server.Close()

	resp, err := http.Post(server.URL, "application/json", strings.NewReader(`{"provider":"github_actions","token":"whatever"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
}

func TestHandler_PollsUntilTerminalThenReports(t *testing.T) {
	origInterval, origTimeout := pollInterval, pollTimeout
	pollInterval, pollTimeout = time.Millisecond, time.Second
	t.Cleanup(func() { pollInterval, pollTimeout = origInterval, origTimeout })

	done := make(chan struct{})
	cp := &fakeResolveClient{resolved: resolvedRun(), reportDone: done}
	registry := NewRegistry(&fakeVerifier{provider: ci.ProviderGitHubActions, claims: Claims{Repo: "acme/widgets", RunID: "42"}})
	h := &Handler{
		registry: registry, cp: cp, broker: &fakeBroker{},
		adapter: &fakeStatusAdapter{statuses: []ci.RunStatus{
			{Status: ci.StatusRunning},
			{Status: ci.StatusSucceeded, Raw: "success", URL: "https://github.com/acme/widgets/actions/runs/42"},
		}},
		githubCreds: fakeGitHubCredential{},
	}

	go h.pollUntilTerminal("acme/widgets", "github_actions", "run-1", "42")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReportRunResult was never called")
	}

	result := cp.lastResult()
	if result.Status != string(ci.StatusSucceeded) {
		t.Errorf("Status = %q, want %q", result.Status, ci.StatusSucceeded)
	}
	if result.CIURL != "https://github.com/acme/widgets/actions/runs/42" {
		t.Errorf("CIURL = %q, want the run's URL", result.CIURL)
	}
}

func TestHandler_PollTimesOut(t *testing.T) {
	origInterval, origTimeout := pollInterval, pollTimeout
	pollInterval, pollTimeout = time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { pollInterval, pollTimeout = origInterval, origTimeout })

	done := make(chan struct{})
	cp := &fakeResolveClient{resolved: resolvedRun(), reportDone: done}
	registry := NewRegistry(&fakeVerifier{provider: ci.ProviderGitHubActions})
	h := &Handler{
		registry: registry, cp: cp, broker: &fakeBroker{},
		adapter:     &fakeStatusAdapter{statuses: []ci.RunStatus{{Status: ci.StatusRunning}}},
		githubCreds: fakeGitHubCredential{},
	}

	go h.pollUntilTerminal("acme/widgets", "github_actions", "run-1", "42")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReportRunResult was never called")
	}

	result := cp.lastResult()
	if result.Status != string(ci.StatusTimedOut) {
		t.Errorf("Status = %q, want %q", result.Status, ci.StatusTimedOut)
	}
}
