package controlplane_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
)

func fakeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestClient_NextJob_ParsesJob(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/worker/verifications/next" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer a-worker-token" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer a-worker-token")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"verification_id": "verification-1",
			"environment":     "staging",
			"provider":        "aws",
			"account_ref":     "123456789012",
			"external_id":     "generated-external-id",
			"trust_anchor":    "arn:aws:iam::123456789012:role/worker",
			"role_arns": map[string]string{
				"read_only": "arn:aws:iam::123456789012:role/tier1",
			},
		})
	})

	job, err := controlplane.New(ts.URL, "a-worker-token").NextJob(t.Context())
	if err != nil {
		t.Fatalf("NextJob: %v", err)
	}
	if job == nil {
		t.Fatal("NextJob returned nil, want a job")
	}
	if job.VerificationID != "verification-1" || job.AccountRef != "123456789012" {
		t.Errorf("unexpected job: %+v", job)
	}
	if job.RoleARNs["read_only"] != "arn:aws:iam::123456789012:role/tier1" {
		t.Errorf("unexpected role arns: %+v", job.RoleARNs)
	}
}

func TestClient_NextJob_NoneAvailable(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	job, err := controlplane.New(ts.URL, "a-worker-token").NextJob(t.Context())
	if err != nil {
		t.Fatalf("NextJob: %v", err)
	}
	if job != nil {
		t.Errorf("NextJob = %+v, want nil", job)
	}
}

func TestClient_NextJob_UnexpectedStatus(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := controlplane.New(ts.URL, "a-worker-token").NextJob(t.Context()); err == nil {
		t.Fatal("NextJob succeeded despite a 401 response")
	}
}

func TestClient_ReportResult_SendsRequest(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/worker/verifications/verification-1/result" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			TierResults map[string]controlplane.TierResult `json:"tier_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if !body.TierResults["read_only"].OK {
			t.Errorf("unexpected tier results: %+v", body.TierResults)
		}
		w.WriteHeader(http.StatusOK)
	})

	err := controlplane.New(ts.URL, "a-worker-token").ReportResult(t.Context(), "verification-1", map[string]controlplane.TierResult{
		"read_only": {OK: true},
	})
	if err != nil {
		t.Fatalf("ReportResult: %v", err)
	}
}

func TestClient_ReportResult_UnexpectedStatus(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	err := controlplane.New(ts.URL, "a-worker-token").ReportResult(t.Context(), "verification-1", map[string]controlplane.TierResult{
		"read_only": {OK: true},
	})
	if err == nil {
		t.Fatal("ReportResult succeeded despite a 404 response")
	}
}
