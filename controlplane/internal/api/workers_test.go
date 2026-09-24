package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

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

// newTestServerWithBootstrap is newTestServer with worker self-registration
// enabled under bootstrapToken.
func newTestServerWithBootstrap(t *testing.T, bootstrapToken string) *httptest.Server {
	t.Helper()
	conn := dbtest.New(t)

	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	srv := api.NewServer(conn, identity.NewIssuer(priv), identity.NewVerifier(pub))
	srv.EnableWorkerBootstrap(bootstrapToken)
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestPostWorkerBootstrap_DisabledByDefault(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", map[string]any{"name": "worker-staging"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d (route should not exist without a configured token)", resp.StatusCode, http.StatusNotFound)
	}
}

func TestPostWorkerBootstrap_WrongToken(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "wrong-token", map[string]any{"name": "worker-staging"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostWorkerBootstrap_MissingToken(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", map[string]any{"name": "worker-staging"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostWorkerBootstrap_BeforeSetupReturnsConflict(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "correct-token", map[string]any{"name": "worker-staging"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

func TestPostWorkerBootstrap_FirstCallMintsUsableZeroEnvironmentToken(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")
	setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "correct-token", map[string]any{"name": "worker-staging"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/workers/bootstrap: status = %d", resp.StatusCode)
	}
	bootstrapped := decodeJSON[struct {
		WorkerCredentialID string `json:"worker_credential_id"`
		Token              string `json:"token"`
	}](t, resp)
	if bootstrapped.WorkerCredentialID == "" || bootstrapped.Token == "" {
		t.Fatalf("incomplete response: %+v", bootstrapped)
	}

	// The minted token actually authenticates, even with zero environments.
	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", bootstrapped.Token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("GET /v1/worker/verifications/next with bootstrapped token: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestPostWorkerBootstrap_RepeatCallRotatesToken(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")
	setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "correct-token", map[string]any{"name": "worker-staging"})
	first := decodeJSON[struct {
		WorkerCredentialID string `json:"worker_credential_id"`
		Token              string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "correct-token", map[string]any{"name": "worker-staging"})
	second := decodeJSON[struct {
		WorkerCredentialID string `json:"worker_credential_id"`
		Token              string `json:"token"`
	}](t, resp)

	if second.WorkerCredentialID != first.WorkerCredentialID {
		t.Errorf("second bootstrap minted a new credential %s, want the same one %s", second.WorkerCredentialID, first.WorkerCredentialID)
	}
	if second.Token == first.Token {
		t.Error("second bootstrap returned the same token, want a rotated one")
	}

	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", first.Token, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old token after rotation: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", second.Token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("new token after rotation: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestPostWorkerGrantEnvironments_FullFlow(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken) // registers "staging"

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "correct-token", map[string]any{"name": "worker-staging"})
	bootstrapped := decodeJSON[struct {
		WorkerCredentialID string `json:"worker_credential_id"`
		Token              string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/"+bootstrapped.WorkerCredentialID+"/environments", adminToken, map[string]any{
		"environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /v1/workers/{id}/environments: status = %d", resp.StatusCode)
	}

	// Granting the same environment again is a no-op, not an error.
	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/"+bootstrapped.WorkerCredentialID+"/environments", adminToken, map[string]any{
		"environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("repeat grant: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestPostWorkerGrantEnvironments_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/workers/"+uuid.NewString()+"/environments", map[string]any{
		"environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostWorkerGrantEnvironments_UnknownWorkerCredential(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/"+uuid.NewString()+"/environments", adminToken, map[string]any{
		"environments": []string{"staging"},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestGetWorkers_ListsByName(t *testing.T) {
	ts := newTestServerWithBootstrap(t, "correct-token")
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken) // registers "staging"

	created := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name": "worker-b", "environments": []string{"staging"},
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling worker-b: status = %d", created.StatusCode)
	}

	bootstrapped := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/bootstrap", "correct-token", map[string]any{"name": "worker-a"})
	if bootstrapped.StatusCode != http.StatusOK {
		t.Fatalf("bootstrapping worker-a: status = %d", bootstrapped.StatusCode)
	}

	resp := doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/workers", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/workers: status = %d", resp.StatusCode)
	}
	list := decodeJSON[[]struct {
		WorkerCredentialID string   `json:"worker_credential_id"`
		Name               string   `json:"name"`
		Environments       []string `json:"environments"`
	}](t, resp)
	if len(list) != 2 {
		t.Fatalf("got %d credentials, want 2", len(list))
	}
	if list[0].Name != "worker-a" || len(list[0].Environments) != 0 {
		t.Errorf("worker-a = %+v, want zero environments", list[0])
	}
	if list[1].Name != "worker-b" || len(list[1].Environments) != 1 || list[1].Environments[0] != "staging" {
		t.Errorf("worker-b = %+v, want [staging]", list[1])
	}
}

func TestGetWorkers_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodGet, ts.URL+"/v1/workers", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostWorkerGrantEnvironments_RejectsEnvironmentOutsideOwnScope(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken) // registers "staging"

	prodBody := validEnvironmentBody()
	prodBody["name"] = "prod"
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", adminToken, prodBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("registering prod environment: status = %d", resp.StatusCode)
	}

	created := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name": "worker-staging", "environments": []string{"staging"},
	})
	worker := decodeJSON[struct {
		WorkerCredentialID string `json:"worker_credential_id"`
	}](t, created)

	// An Autonomous-tier agent scoped only to staging must not be able to
	// grant a worker credential access to prod.
	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "staging-agent", "team": "default", "tier": "autonomous", "environments": []string{"staging"}, "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling staging-scoped agent: status = %d", resp.StatusCode)
	}
	stagingAgent := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers/"+worker.WorkerCredentialID+"/environments", stagingAgent.Token, map[string]any{
		"environments": []string{"prod"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
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
