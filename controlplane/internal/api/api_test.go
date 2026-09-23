package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/session"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	conn := dbtest.New(t)

	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	srv := api.NewServer(conn, identity.NewIssuer(priv), identity.NewVerifier(pub))
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	return v
}

func TestSetupAndLogin_FullFlow(t *testing.T) {
	ts := newTestServer(t)

	// A fresh instance needs setup.
	resp := doJSON(t, http.MethodGet, ts.URL+"/v1/setup", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/setup: status = %d", resp.StatusCode)
	}
	status := decodeJSON[struct {
		NeedsSetup bool `json:"needs_setup"`
	}](t, resp)
	if !status.NeedsSetup {
		t.Fatal("NeedsSetup = false on a fresh instance, want true")
	}

	// Running setup succeeds and returns a usable token.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/setup", map[string]string{
		"tenant_name":    "acme-corp",
		"admin_username": "admin",
		"admin_password": "correct-horse-battery-staple",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/setup: status = %d", resp.StatusCode)
	}
	setup := decodeJSON[struct {
		TenantID string `json:"tenant_id"`
		ActorID  string `json:"actor_id"`
		Token    string `json:"token"`
	}](t, resp)
	if setup.Token == "" {
		t.Fatal("setup response had no token")
	}

	// The instance now reports setup as done.
	resp = doJSON(t, http.MethodGet, ts.URL+"/v1/setup", nil)
	status = decodeJSON[struct {
		NeedsSetup bool `json:"needs_setup"`
	}](t, resp)
	if status.NeedsSetup {
		t.Error("NeedsSetup = true after setup ran, want false")
	}

	// Setup refuses permanently.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/setup", map[string]string{
		"tenant_name":    "someone-elses-corp",
		"admin_username": "admin2",
		"admin_password": "another-long-enough-password",
	})
	if resp.StatusCode != http.StatusGone {
		t.Errorf("second POST /v1/setup: status = %d, want %d", resp.StatusCode, http.StatusGone)
	}

	// Logging in with the credentials just set works.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/token", map[string]string{
		"username": "admin",
		"password": "correct-horse-battery-staple",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/token: status = %d", resp.StatusCode)
	}
	tok := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)
	if tok.Token == "" {
		t.Error("token response had no token")
	}

	// A wrong password is rejected.
	resp = doJSON(t, http.MethodPost, ts.URL+"/v1/token", map[string]string{
		"username": "admin",
		"password": "wrong-password",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostSetup_ValidatesRequest(t *testing.T) {
	ts := newTestServer(t)

	cases := []struct {
		name string
		body map[string]string
	}{
		{"missing tenant name", map[string]string{"admin_username": "admin", "admin_password": "correct-horse-battery-staple"}},
		{"missing admin username", map[string]string{"tenant_name": "acme-corp", "admin_password": "correct-horse-battery-staple"}},
		{"short password", map[string]string{"tenant_name": "acme-corp", "admin_username": "admin", "admin_password": "short"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, http.MethodPost, ts.URL+"/v1/setup", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestPostToken_UnknownUsername(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/token", map[string]string{
		"username": "nobody",
		"password": "whatever-password",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// The issued token actually verifies as a real, usable session — end to end
// through pkg/identity, not just "the API returned 200".
func TestSetup_IssuedTokenVerifies(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	verifier := identity.NewVerifier(pub)

	srv := api.NewServer(conn, issuer, verifier)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/setup", map[string]string{
		"tenant_name":    "acme-corp",
		"admin_username": "admin",
		"admin_password": "correct-horse-battery-staple",
	})
	setup := decodeJSON[struct {
		TenantID string `json:"tenant_id"`
		ActorID  string `json:"actor_id"`
		Token    string `json:"token"`
	}](t, resp)

	checker := session.NewStore(conn)
	claims, err := verifier.Verify(context.Background(), setup.Token, checker)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.ActorID != setup.ActorID {
		t.Errorf("claims.ActorID = %s, want %s", claims.ActorID, setup.ActorID)
	}
	if claims.Tier != identity.TierAutonomous {
		t.Errorf("claims.Tier = %d, want %d", claims.Tier, identity.TierAutonomous)
	}
}
