package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func newExecutionEnabledServer(t *testing.T) *httptest.Server {
	t.Helper()
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv := api.NewServer(conn, identity.NewIssuer(priv), identity.NewVerifier(pub))
	pipelines := pipeline.NewStore(conn)
	runs := execution.NewStore(conn, pipelines, fakeEnqueuer{})
	srv.EnableExecution(runs)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

// mustCreateEnvironment registers the "staging" environment, authenticated
// as adminToken, and returns its ID.
func mustCreateEnvironment(t *testing.T, ts *httptest.Server, adminToken string) string {
	t.Helper()
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", adminToken, map[string]any{
		"name":         "staging",
		"provider":     "aws",
		"region":       "us-east-1",
		"account_ref":  "123456789012",
		"external_id":  "ext-id",
		"trust_anchor": "trust-anchor",
		"role_arns": map[string]string{
			"read_only":         "arn:aws:iam::123456789012:role/read-only",
			"human_in_the_loop": "arn:aws:iam::123456789012:role/human-in-the-loop",
			"autonomous":        "arn:aws:iam::123456789012:role/autonomous",
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/environments: status = %d", resp.StatusCode)
	}
	return decodeJSON[struct {
		EnvironmentID string `json:"environment_id"`
	}](t, resp).EnvironmentID
}

// requestRunAsAdmin creates an environment and a non-mutating pipeline (so
// the admin's Autonomous tier auto-queues the run without needing an
// approval), requests a run against it, and returns the new run's ID.
func requestRunAsAdmin(t *testing.T, ts *httptest.Server, adminToken string) string {
	t.Helper()
	environmentID := mustCreateEnvironment(t, ts, adminToken)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/pipelines", adminToken, map[string]any{
		"environment_id": environmentID,
		"provider":       "github_actions",
		"workflow_ref":   ".github/workflows/deploy.yml",
		"mutating":       false,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/pipelines: status = %d", resp.StatusCode)
	}
	pipelineID := decodeJSON[struct {
		PipelineID string `json:"pipeline_id"`
	}](t, resp).PipelineID

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/runs", adminToken, map[string]any{
		"environment_id": environmentID,
		"pipeline_id":    pipelineID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/runs: status = %d", resp.StatusCode)
	}
	return decodeJSON[struct {
		RunID string `json:"run_id"`
	}](t, resp).RunID
}

func TestGetRunCost_UnknownRun_NotFound(t *testing.T) {
	ts := newExecutionEnabledServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/runs/"+uuid.NewString()+"/cost", adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestGetRunCost_NoCostCapturedYet_EmptyList(t *testing.T) {
	ts := newExecutionEnabledServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	runID := requestRunAsAdmin(t, ts, adminToken)

	resp := doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/runs/"+runID+"/cost", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	rows := decodeJSON[[]struct {
		Source string `json:"source"`
	}](t, resp)
	if len(rows) != 0 {
		t.Errorf("expected no cost rows yet, got %d", len(rows))
	}
}

func TestPutCostRate_ReadOnlyTier_Forbidden(t *testing.T) {
	ts := newExecutionEnabledServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	mustCreateEnvironment(t, ts, adminToken)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name":         "read-only-agent",
		"team":         "default",
		"tier":         "read_only",
		"environments": []string{"staging"},
		"ttl_seconds":  3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling read-only agent: status = %d", resp.StatusCode)
	}
	readOnlyToken := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp).Token

	resp = doAuthedJSON(t, http.MethodPut, ts.URL+"/v1/cost-rates", readOnlyToken, map[string]any{
		"rate_micros_per_ms": 100,
		"currency":           "USD",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPutCostRate_AutonomousTier_Succeeds(t *testing.T) {
	ts := newExecutionEnabledServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPut, ts.URL+"/v1/cost-rates", adminToken, map[string]any{
		"rate_micros_per_ms": 100,
		"currency":           "USD",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
