package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectCIContext(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "acme/widgets")
	provider, repo, err := detectCIContext()
	if err != nil {
		t.Fatalf("detectCIContext: %v", err)
	}
	if provider != "github_actions" {
		t.Errorf("provider = %q, want %q", provider, "github_actions")
	}
	if repo != "acme/widgets" {
		t.Errorf("repo = %q, want %q", repo, "acme/widgets")
	}
}

func TestDetectCIContext_Unsupported(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	if _, _, err := detectCIContext(); err == nil {
		t.Fatal("detectCIContext succeeded with no supported platform detected")
	}
}

func TestDetectCIContext_MissingRepo(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "")
	if _, _, err := detectCIContext(); err == nil {
		t.Fatal("detectCIContext succeeded with no GITHUB_REPOSITORY set")
	}
}

func TestFetchGitHubOIDCToken_Success(t *testing.T) {
	var gotAudience, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAudience = r.URL.Query().Get("audience")
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "fake-oidc-token"})
	}))
	defer server.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", server.URL+"/token?")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-token")

	token, err := fetchGitHubOIDCToken(context.Background(), "https://idp-worker.example.com/ci/callback")
	if err != nil {
		t.Fatalf("fetchGitHubOIDCToken: %v", err)
	}
	if token != "fake-oidc-token" { // #nosec G101 -- test fixture, not a real credential
		t.Errorf("token = %q, want %q", token, "fake-oidc-token")
	}
	if gotAudience != "https://idp-worker.example.com/ci/callback" {
		t.Errorf("audience = %q, want the callback url", gotAudience)
	}
	if gotAuth != "Bearer runner-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer runner-token")
	}
}

func TestFetchGitHubOIDCToken_MissingEnv(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")

	if _, err := fetchGitHubOIDCToken(context.Background(), "aud"); err == nil {
		t.Fatal("fetchGitHubOIDCToken succeeded with no ACTIONS_ID_TOKEN_REQUEST_URL/_TOKEN set")
	}
}

func TestPostCallback_Success(t *testing.T) {
	var gotBody struct {
		Provider string `json:"provider"`
		Token    string `json:"token"`
	}
	expiry := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(credentials{ // #nosec G117 -- test fixture, not a real credential
			AccessKeyID: "AKIATEST", SecretAccessKey: "secret", SessionToken: "token", Expiration: expiry, // pragma: allowlist secret
		})
	}))
	defer server.Close()

	creds, err := postCallback(context.Background(), server.URL, "github_actions", "a-token")
	if err != nil {
		t.Fatalf("postCallback: %v", err)
	}
	if gotBody.Provider != "github_actions" || gotBody.Token != "a-token" {
		t.Errorf("request body = %+v, want provider/token forwarded", gotBody)
	}
	if creds.AccessKeyID != "AKIATEST" {
		t.Errorf("AccessKeyID = %q, want %q", creds.AccessKeyID, "AKIATEST")
	}
	if !creds.Expiration.Equal(expiry) {
		t.Errorf("Expiration = %v, want %v", creds.Expiration, expiry)
	}
}

func TestPostCallback_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	if _, err := postCallback(context.Background(), server.URL, "github_actions", "a-token"); err == nil {
		t.Fatal("postCallback succeeded despite a non-200 response")
	}
}

func testCreds() credentials {
	return credentials{
		AccessKeyID: "AKIATEST", SecretAccessKey: "secret", SessionToken: "token",
		Expiration: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, testCreds()); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	var got credentials
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got != testCreds() {
		t.Errorf("got %+v, want %+v", got, testCreds())
	}
}

func TestWriteEnv(t *testing.T) {
	var buf bytes.Buffer
	if err := writeEnv(&buf, testCreds()); err != nil {
		t.Fatalf("writeEnv: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"export AWS_ACCESS_KEY_ID='AKIATEST'",
		"export AWS_SECRET_ACCESS_KEY='secret'", // pragma: allowlist secret
		"export AWS_SESSION_TOKEN='token'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

func TestWriteEnv_EscapesShellMetacharacters(t *testing.T) {
	var buf bytes.Buffer
	malicious := credentials{
		AccessKeyID:     "AKIA'; rm -rf / #",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Expiration:      testCreds().Expiration,
	}
	if err := writeEnv(&buf, malicious); err != nil {
		t.Fatalf("writeEnv: %v", err)
	}
	if want := `export AWS_ACCESS_KEY_ID='AKIA'\''; rm -rf / #'`; !strings.Contains(buf.String(), want) {
		t.Errorf("output = %q, want the single quote escaped as %q", buf.String(), want)
	}
}

func TestWriteCredentialProcess(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCredentialProcess(&buf, testCreds()); err != nil {
		t.Fatalf("writeCredentialProcess: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got["Version"] != float64(1) {
		t.Errorf("Version = %v, want 1", got["Version"])
	}
	if got["AccessKeyId"] != "AKIATEST" {
		t.Errorf("AccessKeyId = %v, want %q", got["AccessKeyId"], "AKIATEST")
	}
	if got["Expiration"] != "2026-01-01T12:00:00Z" {
		t.Errorf("Expiration = %v, want RFC3339", got["Expiration"])
	}
}

func TestRunCIAuth_UnknownFormat(t *testing.T) {
	if err := runCIAuth(context.Background(), "http://localhost:8080", "yaml"); err == nil {
		t.Fatal("runCIAuth succeeded with an unsupported --format")
	}
}
