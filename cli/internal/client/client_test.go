package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
)

// fakeServer is a hand-written stand-in for the control plane, exercising
// only the wire contract (request shape, response shape, error envelope) —
// client can't import controlplane/internal/api to test against the real
// thing, by the same internal-package rule that stops the reverse.
func fakeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encoding fake response: %v", err)
	}
}

func TestClient_NeedsSetup(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/setup" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]bool{"needs_setup": true})
	})

	got, err := client.New(ts.URL).NeedsSetup(t.Context())
	if err != nil {
		t.Fatalf("NeedsSetup: %v", err)
	}
	if !got {
		t.Error("NeedsSetup = false, want true")
	}
}

func TestClient_Setup_SendsRequestAndParsesResponse(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/setup" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var got client.SetupRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if got.TenantName != "acme-corp" || got.AdminUsername != "admin" || got.AdminPassword != "correct-horse-battery-staple" {
			t.Errorf("unexpected request body: %+v", got)
		}
		writeJSON(t, w, http.StatusCreated, client.SetupResponse{
			TenantID: "tenant-1", ActorID: "actor-1", Token: "a-token",
		})
	})

	resp, err := client.New(ts.URL).Setup(t.Context(), client.SetupRequest{
		TenantName:    "acme-corp",
		AdminUsername: "admin",
		AdminPassword: "correct-horse-battery-staple",
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if resp.Token != "a-token" || resp.ActorID != "actor-1" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestClient_CreateAgent_SetsAuthorizationHeader(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer a-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer a-token")
		}
		writeJSON(t, w, http.StatusCreated, client.CreateAgentResponse{ActorID: "agent-1", Token: "agent-token"})
	})

	resp, err := client.New(ts.URL).CreateAgent(t.Context(), "a-token", client.CreateAgentRequest{
		Name: "claude-code-rhys", Team: "default", Tier: "autonomous", TTLSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if resp.ActorID != "agent-1" {
		t.Errorf("ActorID = %q, want %q", resp.ActorID, "agent-1")
	}
}

func TestClient_CreateWorker_SendsRequestAndParsesResponse(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workers" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var got client.CreateWorkerRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if got.Name != "worker-staging" || len(got.Environments) != 1 || got.Environments[0] != "staging" {
			t.Errorf("unexpected request body: %+v", got)
		}
		writeJSON(t, w, http.StatusCreated, client.CreateWorkerResponse{
			WorkerCredentialID: "worker-cred-1", Token: "worker-token",
		})
	})

	resp, err := client.New(ts.URL).CreateWorker(t.Context(), "a-token", client.CreateWorkerRequest{
		Name: "worker-staging", Environments: []string{"staging"},
	})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	if resp.WorkerCredentialID != "worker-cred-1" || resp.Token != "worker-token" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestClient_TriggerVerification_SendsRequest(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/environments/staging/verify" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusCreated, client.TriggerVerificationResponse{
			VerificationID: "verification-1", Status: "pending",
		})
	})

	resp, err := client.New(ts.URL).TriggerVerification(t.Context(), "a-token", "staging")
	if err != nil {
		t.Fatalf("TriggerVerification: %v", err)
	}
	if resp.VerificationID != "verification-1" || resp.Status != "pending" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestClient_GetVerification_ParsesTierResults(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/environments/staging/verifications/verification-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, client.GetVerificationResponse{
			VerificationID: "verification-1",
			Status:         "failed",
			TierResults: map[string]client.TierResult{
				"read_only":  {OK: true},
				"autonomous": {OK: false, Error: "AccessDenied"},
			},
		})
	})

	resp, err := client.New(ts.URL).GetVerification(t.Context(), "a-token", "staging", "verification-1")
	if err != nil {
		t.Fatalf("GetVerification: %v", err)
	}
	if resp.Status != "failed" {
		t.Errorf("Status = %q, want %q", resp.Status, "failed")
	}
	if resp.TierResults["autonomous"].Error != "AccessDenied" {
		t.Errorf("unexpected tier result: %+v", resp.TierResults["autonomous"])
	}
}

func TestClient_CreateAgent_SendsIdempotencyKey(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var got client.CreateAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if got.IdempotencyKey != "retry-key-1" {
			t.Errorf("IdempotencyKey = %q, want %q", got.IdempotencyKey, "retry-key-1")
		}
		writeJSON(t, w, http.StatusOK, client.CreateAgentResponse{ActorID: "agent-1", Token: "agent-token"})
	})

	_, err := client.New(ts.URL).CreateAgent(t.Context(), "a-token", client.CreateAgentRequest{
		Name: "claude-code-rhys", Team: "default", Tier: "autonomous", TTLSeconds: 3600,
		IdempotencyKey: "retry-key-1",
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
}

func TestClient_DecodesStructuredError(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]string{
			"code": "unauthorized", "message": "incorrect username or password",
		})
	})

	_, err := client.New(ts.URL).Token(t.Context(), client.TokenRequest{Username: "admin", Password: "wrong"})

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want an *client.APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnauthorized)
	}
	if apiErr.Code != "unauthorized" {
		t.Errorf("Code = %q, want %q", apiErr.Code, "unauthorized")
	}
}
