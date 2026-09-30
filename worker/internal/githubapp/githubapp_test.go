package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// testKeyPEM generates a fresh RSA private key PEM for each test — cheap
// enough at test key sizes, and avoids a checked-in fixture key.
func testKeyPEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block)), key
}

func TestNew_InvalidPEM(t *testing.T) {
	if _, err := New("12345", "not a pem"); err == nil {
		t.Fatal("New succeeded with an invalid PEM, want an error")
	}
}

func TestInstallationToken_Success(t *testing.T) {
	pemStr, key := testKeyPEM(t)

	var gotInstallationAuth, gotTokenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/widgets/installation":
			gotInstallationAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 4242})
		case "/app/installations/4242/access_tokens":
			gotTokenAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_installation-token"})
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r, err := New("app-123", pemStr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.baseURL = server.URL

	secret, err := r.InstallationToken(context.Background(), "acme/widgets")
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}
	if secret.Reveal() != "ghs_installation-token" {
		t.Errorf("token = %q, want %q", secret.Reveal(), "ghs_installation-token")
	}

	for _, got := range []string{gotInstallationAuth, gotTokenAuth} {
		if !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("Authorization = %q, want a Bearer JWT", got)
		}
		raw := strings.TrimPrefix(got, "Bearer ")
		claims := jwt.RegisteredClaims{}
		if _, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }); err != nil {
			t.Fatalf("the app JWT does not verify against its own key: %v", err)
		}
		if claims.Issuer != "app-123" {
			t.Errorf("jwt issuer = %q, want %q", claims.Issuer, "app-123")
		}
	}
}

func TestInstallationToken_InstallationNotFound(t *testing.T) {
	pemStr, _ := testKeyPEM(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
	}))
	defer server.Close()

	r, err := New("app-123", pemStr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.baseURL = server.URL

	if _, err := r.InstallationToken(context.Background(), "acme/widgets"); err == nil {
		t.Fatal("InstallationToken succeeded despite the App not being installed on the repo")
	}
}
