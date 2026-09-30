package githubactions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
)

func newTestAdapter(handler http.HandlerFunc) (*Adapter, *httptest.Server) {
	server := httptest.NewServer(handler)
	return &Adapter{baseURL: server.URL, httpClient: server.Client()}, server
}

func validConfig() ci.Config {
	return ci.Config{
		Provider:   ci.ProviderGitHubActions,
		Settings:   map[string]string{SettingRepo: "acme/widgets"},
		Credential: ci.Secret("test-token"),
	}
}

func TestProvider(t *testing.T) {
	if got := New().Provider(); got != ci.ProviderGitHubActions {
		t.Errorf("Provider() = %q, want %q", got, ci.ProviderGitHubActions)
	}
}

func TestCapabilities(t *testing.T) {
	got := New().Capabilities()
	if !got.OIDCCallback {
		t.Error("OIDCCallback = false, want true — correlation relies on the CI OIDC callback for this provider")
	}
	if got.TriggerReturnsID {
		t.Error("TriggerReturnsID = true, want false — workflow_dispatch returns 204 with no run ID")
	}
	if got.Logs {
		t.Error("Logs = true, want false — unsupported for v1")
	}
}

func TestValidateConfig(t *testing.T) {
	a := New()

	if err := a.ValidateConfig(context.Background(), validConfig()); err != nil {
		t.Errorf("ValidateConfig(valid) = %v, want nil", err)
	}

	cfg := validConfig()
	delete(cfg.Settings, SettingRepo)
	if err := a.ValidateConfig(context.Background(), cfg); !errors.Is(err, ci.ErrInvalidConfig) {
		t.Errorf("ValidateConfig(missing repo) = %v, want ErrInvalidConfig", err)
	}

	cfg = validConfig()
	cfg.Provider = ci.ProviderGitLabCI
	if err := a.ValidateConfig(context.Background(), cfg); !errors.Is(err, ci.ErrInvalidConfig) {
		t.Errorf("ValidateConfig(wrong provider) = %v, want ErrInvalidConfig", err)
	}
}

func TestTrigger_Success(t *testing.T) {
	var gotPath, gotAuth string
	a, server := newTestAdapter(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()

	h, err := a.Trigger(context.Background(), validConfig(), ci.TriggerRequest{
		Ref:         "main",
		Workflow:    "deploy.yml",
		Correlation: "run-123",
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if h.Resolved {
		t.Error("Handle.Resolved = true, want false — GitHub gives no run ID back")
	}
	if h.Correlation != "run-123" {
		t.Errorf("Handle.Correlation = %q, want %q", h.Correlation, "run-123")
	}
	if want := "/repos/acme/widgets/actions/workflows/deploy.yml/dispatches"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
}

func TestTrigger_GitHubError(t *testing.T) {
	a, server := newTestAdapter(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(githubError{Message: "workflow does not have workflow_dispatch trigger"})
	})
	defer server.Close()

	_, err := a.Trigger(context.Background(), validConfig(), ci.TriggerRequest{Ref: "main", Workflow: "deploy.yml"})
	if err == nil {
		t.Fatal("Trigger succeeded despite a GitHub API error")
	}
}

func TestResolve_Unsupported(t *testing.T) {
	a := New()
	h := ci.Handle{Correlation: "run-123"}

	got, err := a.Resolve(context.Background(), validConfig(), h)
	if !errors.Is(err, ci.ErrUnsupported) {
		t.Errorf("Resolve error = %v, want ErrUnsupported", err)
	}
	if got != h {
		t.Errorf("Resolve returned %+v, want the handle unchanged", got)
	}
}

func TestStatus_NotResolved(t *testing.T) {
	a := New()
	_, err := a.Status(context.Background(), validConfig(), ci.Handle{Resolved: false})
	if !errors.Is(err, ci.ErrNotResolved) {
		t.Errorf("Status error = %v, want ErrNotResolved", err)
	}
}

func TestStatus_Mapping(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		conclusion string
		want       ci.Status
	}{
		{"queued", "queued", "", ci.StatusPending},
		{"in_progress", "in_progress", "", ci.StatusRunning},
		{"succeeded", "completed", "success", ci.StatusSucceeded},
		{"failed", "completed", "failure", ci.StatusFailed},
		{"cancelled", "completed", "cancelled", ci.StatusCancelled},
		{"timed_out", "completed", "timed_out", ci.StatusTimedOut},
		{"other_conclusion_is_failure", "completed", "neutral", ci.StatusFailed},
		{"unknown_status", "some_new_status", "", ci.StatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, server := newTestAdapter(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(runResponse{Status: tc.status, Conclusion: tc.conclusion, HTMLURL: "https://github.com/acme/widgets/actions/runs/42"})
			})
			defer server.Close()

			got, err := a.Status(context.Background(), validConfig(), ci.Handle{Resolved: true, ExternalID: "42"})
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if got.Status != tc.want {
				t.Errorf("Status = %q, want %q", got.Status, tc.want)
			}
			if got.URL != "https://github.com/acme/widgets/actions/runs/42" {
				t.Errorf("URL = %q, want the run's html_url", got.URL)
			}
		})
	}
}

func TestCancel(t *testing.T) {
	var gotPath string
	a, server := newTestAdapter(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	})
	defer server.Close()

	if err := a.Cancel(context.Background(), validConfig(), ci.Handle{Resolved: true, ExternalID: "42"}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if want := "/repos/acme/widgets/actions/runs/42/cancel"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestCancel_NotResolved(t *testing.T) {
	a := New()
	if err := a.Cancel(context.Background(), validConfig(), ci.Handle{Resolved: false}); !errors.Is(err, ci.ErrNotResolved) {
		t.Errorf("Cancel error = %v, want ErrNotResolved", err)
	}
}

func TestLogs_Unsupported(t *testing.T) {
	a := New()
	_, err := a.Logs(context.Background(), validConfig(), ci.Handle{Resolved: true, ExternalID: "42"}, "")
	if !errors.Is(err, ci.ErrUnsupported) {
		t.Errorf("Logs error = %v, want ErrUnsupported", err)
	}
}
