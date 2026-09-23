package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func registerEnvironment(t *testing.T, ts *httptest.Server, adminToken string) {
	t.Helper()
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", adminToken, validEnvironmentBody())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("registering prerequisite environment: status = %d", resp.StatusCode)
	}
}

func TestPostWorkers_FullFlow(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv := api.NewServer(conn, identity.NewIssuer(priv), identity.NewVerifier(pub))
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name":         "worker-staging",
		"environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/workers: status = %d", resp.StatusCode)
	}
	created := decodeJSON[struct {
		WorkerCredentialID string `json:"worker_credential_id"`
		Token              string `json:"token"`
	}](t, resp)
	if created.WorkerCredentialID == "" || created.Token == "" {
		t.Fatalf("incomplete response: %+v", created)
	}

	// The minted token actually authenticates on a worker-only endpoint.
	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", created.Token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("GET /v1/worker/verifications/next with fresh token: status = %d, want %d (nothing pending)", resp.StatusCode, http.StatusNoContent)
	}
}

func TestPostWorkers_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/workers", map[string]any{
		"name": "worker-staging", "environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostWorkers_RequiresAutonomousTier(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "supervised-agent", "team": "default", "tier": "human_in_the_loop", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling supervised agent: status = %d", resp.StatusCode)
	}
	supervised := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", supervised.Token, map[string]any{
		"name": "worker-staging", "environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPostWorkers_UnknownEnvironment(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name": "worker-staging", "environments": []string{"does-not-exist"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPostWorkers_RequiresAtLeastOneEnvironment(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name": "worker-staging", "environments": []string{},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPostWorkers_RejectsEnvironmentOutsideOwnScope(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken) // registers "staging"

	prodBody := validEnvironmentBody()
	prodBody["name"] = "prod"
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", adminToken, prodBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("registering prod environment: status = %d", resp.StatusCode)
	}

	// An Autonomous-tier agent scoped only to staging must not be able to
	// enrol a worker credential covering prod.
	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "staging-agent", "team": "default", "tier": "autonomous", "environments": []string{"staging"}, "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling staging-scoped agent: status = %d", resp.StatusCode)
	}
	stagingAgent := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", stagingAgent.Token, map[string]any{
		"name": "worker-prod", "environments": []string{"prod"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPostWorkers_RootActorGrantsAnyEnvironment(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken)

	// The root actor holds no environments of its own, yet must still be
	// able to scope a worker credential to any environment in the tenant.
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name": "worker-staging", "environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}
