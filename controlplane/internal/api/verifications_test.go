package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// enrolWorker enrols a worker named workerName, scoped to envName, returning
// its bearer token.
func enrolWorker(t *testing.T, ts *httptest.Server, adminToken, envName, workerName string) string {
	t.Helper()
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/workers", adminToken, map[string]any{
		"name":         workerName,
		"environments": []string{envName},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling prerequisite worker: status = %d", resp.StatusCode)
	}
	created := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)
	return created.Token
}

func TestVerification_FullFlow(t *testing.T) {
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
	workerToken := enrolWorker(t, ts, adminToken, "staging", "worker-staging")

	// 1. An actor triggers the check.
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments/staging/verify", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST .../verify: status = %d", resp.StatusCode)
	}
	triggered := decodeJSON[struct {
		VerificationID string `json:"verification_id"`
		Status         string `json:"status"`
	}](t, resp)
	if triggered.Status != "pending" {
		t.Errorf("initial status = %q, want %q", triggered.Status, "pending")
	}

	// 2. The worker claims it and gets everything needed to run the check.
	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", workerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../next: status = %d", resp.StatusCode)
	}
	job := decodeJSON[struct {
		VerificationID string            `json:"verification_id"`
		Environment    string            `json:"environment"`
		Provider       string            `json:"provider"`
		AccountRef     string            `json:"account_ref"`
		ExternalID     string            `json:"external_id"`
		TrustAnchor    string            `json:"trust_anchor"`
		RoleARNs       map[string]string `json:"role_arns"`
	}](t, resp)
	if job.VerificationID != triggered.VerificationID {
		t.Errorf("claimed %s, want %s", job.VerificationID, triggered.VerificationID)
	}
	if job.AccountRef != "123456789012" || job.ExternalID != "generated-external-id" {
		t.Errorf("unexpected job config: %+v", job)
	}
	if len(job.RoleARNs) != 3 {
		t.Errorf("role_arns = %v, want all 3 tiers", job.RoleARNs)
	}

	// 3. The worker reports its result.
	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/worker/verifications/"+job.VerificationID+"/result", workerToken, map[string]any{
		"tier_results": map[string]any{
			"read_only":         map[string]any{"ok": true},
			"human_in_the_loop": map[string]any{"ok": true},
			"autonomous":        map[string]any{"ok": true},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST .../result: status = %d", resp.StatusCode)
	}

	// 4. The triggering actor polls and sees the completed result.
	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/environments/staging/verifications/"+triggered.VerificationID, adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET .../verifications/{id}: status = %d", resp.StatusCode)
	}
	final := decodeJSON[struct {
		Status      string                    `json:"status"`
		TierResults map[string]map[string]any `json:"tier_results"`
	}](t, resp)
	if final.Status != "succeeded" {
		t.Errorf("final status = %q, want %q", final.Status, "succeeded")
	}
	if len(final.TierResults) != 3 {
		t.Errorf("tier_results = %v, want all 3 tiers", final.TierResults)
	}
}

func TestGetNextWorkerVerification_NoneAvailable(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken)
	workerToken := enrolWorker(t, ts, adminToken, "staging", "worker-staging")

	resp := doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", workerToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func TestGetNextWorkerVerification_RequiresWorkerAuth(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	// An actor token (not a worker credential) must not authenticate here.
	resp := doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", adminToken, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostEnvironmentVerify_UnknownEnvironment(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments/does-not-exist/verify", adminToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestPostWorkerVerificationResult_WrongClaimant(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)
	registerEnvironment(t, ts, adminToken)
	workerToken := enrolWorker(t, ts, adminToken, "staging", "worker-staging")

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments/staging/verify", adminToken, nil)
	triggered := decodeJSON[struct {
		VerificationID string `json:"verification_id"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/worker/verifications/next", workerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claiming: status = %d", resp.StatusCode)
	}

	// A second, unrelated worker credential must not be able to complete a
	// job the first worker claimed.
	imposterToken := enrolWorker(t, ts, adminToken, "staging", "worker-staging-imposter")
	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/worker/verifications/"+triggered.VerificationID+"/result", imposterToken, map[string]any{
		"tier_results": map[string]any{"read_only": map[string]any{"ok": true}},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
