package githuboidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testAudience = "https://idp.example.com/ci/callback"

// fakeIssuer runs a minimal OIDC discovery + JWKS server backed by one RSA
// key, and can sign tokens as if it were GitHub's own issuer.
type fakeIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	f := &fakeIssuer{key: key, kid: "test-key-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   f.server.URL,
			"jwks_uri": f.server.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": f.kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// sign issues a token as this fake issuer, with the given custom claims
// merged over the standard registered ones.
func (f *fakeIssuer) sign(t *testing.T, audience string, custom map[string]any) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": f.server.URL,
		"aud": audience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range custom {
		claims[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = f.kid
	signed, err := token.SignedString(f.key)
	if err != nil {
		t.Fatalf("signing test token: %v", err)
	}
	return signed
}

func testClaims() map[string]any {
	return map[string]any{
		"repository":   "acme/widgets",
		"workflow_ref": "acme/widgets/.github/workflows/deploy.yml@refs/heads/main",
		"ref":          "refs/heads/main",
		"run_id":       "42",
		"actor":        "octocat",
	}
}

func TestVerify_Success(t *testing.T) {
	issuer := newFakeIssuer(t)
	v, err := newWithIssuer(context.Background(), issuer.server.URL, testAudience)
	if err != nil {
		t.Fatalf("newWithIssuer: %v", err)
	}

	token := issuer.sign(t, testAudience, testClaims())
	claims, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Repo != "acme/widgets" {
		t.Errorf("Repo = %q, want %q", claims.Repo, "acme/widgets")
	}
	if claims.WorkflowRef != ".github/workflows/deploy.yml" {
		t.Errorf("WorkflowRef = %q, want %q", claims.WorkflowRef, ".github/workflows/deploy.yml")
	}
	if claims.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q, want %q", claims.Ref, "refs/heads/main")
	}
	if claims.RunID != "42" {
		t.Errorf("RunID = %q, want %q", claims.RunID, "42")
	}
	if claims.Actor != "octocat" {
		t.Errorf("Actor = %q, want %q", claims.Actor, "octocat")
	}
	if want := "https://github.com/acme/widgets/actions/runs/42"; claims.URL != want {
		t.Errorf("URL = %q, want %q", claims.URL, want)
	}
}

func TestVerify_WrongAudience(t *testing.T) {
	issuer := newFakeIssuer(t)
	v, err := newWithIssuer(context.Background(), issuer.server.URL, testAudience)
	if err != nil {
		t.Fatalf("newWithIssuer: %v", err)
	}

	token := issuer.sign(t, "some-other-service", testClaims())
	if _, err := v.Verify(context.Background(), token); err == nil {
		t.Fatal("Verify succeeded with a token minted for a different audience")
	}
}

func TestVerify_WrongSigningKey(t *testing.T) {
	issuer := newFakeIssuer(t)
	v, err := newWithIssuer(context.Background(), issuer.server.URL, testAudience)
	if err != nil {
		t.Fatalf("newWithIssuer: %v", err)
	}

	impostorKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating impostor key: %v", err)
	}
	claims := jwt.MapClaims{"iss": issuer.server.URL, "aud": testAudience, "exp": time.Now().Add(time.Minute).Unix()}
	for k, v := range testClaims() {
		claims[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = issuer.kid
	signed, err := token.SignedString(impostorKey)
	if err != nil {
		t.Fatalf("signing with impostor key: %v", err)
	}

	if _, err := v.Verify(context.Background(), signed); err == nil {
		t.Fatal("Verify succeeded with a token signed by the wrong key")
	}
}

func TestVerify_Expired(t *testing.T) {
	issuer := newFakeIssuer(t)
	v, err := newWithIssuer(context.Background(), issuer.server.URL, testAudience)
	if err != nil {
		t.Fatalf("newWithIssuer: %v", err)
	}

	claims := jwt.MapClaims{"iss": issuer.server.URL, "aud": testAudience, "exp": time.Now().Add(-time.Minute).Unix()}
	for k, v := range testClaims() {
		claims[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = issuer.kid
	signed, err := token.SignedString(issuer.key)
	if err != nil {
		t.Fatalf("signing expired token: %v", err)
	}

	if _, err := v.Verify(context.Background(), signed); err == nil {
		t.Fatal("Verify succeeded with an expired token")
	}
}

func TestParseWorkflowRef(t *testing.T) {
	cases := []struct {
		repo, workflowRef, want string
	}{
		{"acme/widgets", "acme/widgets/.github/workflows/deploy.yml@refs/heads/main", ".github/workflows/deploy.yml"},
		{"acme/widgets", "acme/widgets/.github/workflows/deploy.yml@refs/tags/v1.0.0", ".github/workflows/deploy.yml"},
	}
	for _, tc := range cases {
		if got := parseWorkflowRef(tc.repo, tc.workflowRef); got != tc.want {
			t.Errorf("parseWorkflowRef(%q, %q) = %q, want %q", tc.repo, tc.workflowRef, got, tc.want)
		}
	}
}
