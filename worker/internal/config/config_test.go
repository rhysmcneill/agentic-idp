package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rhysmcneill/agentic-idp/worker/internal/config"
)

// fakeEnv returns a getenv func backed by m, for testing without touching
// real process environment variables.
func fakeEnv(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func TestLoad_TokenFromEnv(t *testing.T) {
	cfg, err := config.Load(fakeEnv(map[string]string{
		"IDP_CONTROL_PLANE_URL": "http://localhost:8080/",
		"IDP_WORKER_TOKEN":      "a-worker-token",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ControlPlaneURL != "http://localhost:8080" {
		t.Errorf("ControlPlaneURL = %q, want trailing slash trimmed", cfg.ControlPlaneURL)
	}
	if cfg.Token != "a-worker-token" {
		t.Errorf("Token = %q, want %q", cfg.Token, "a-worker-token")
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("PollInterval = %v, want the 5s default", cfg.PollInterval)
	}
}

func TestLoad_TokenFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("a-worker-token\n"), 0o600); err != nil {
		t.Fatalf("writing token file: %v", err)
	}

	cfg, err := config.Load(fakeEnv(map[string]string{
		"IDP_CONTROL_PLANE_URL": "http://localhost:8080",
		"IDP_WORKER_TOKEN_FILE": path,
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "a-worker-token" {
		t.Errorf("Token = %q, want %q (trimmed)", cfg.Token, "a-worker-token")
	}
}

func TestLoad_MissingControlPlaneURL(t *testing.T) {
	_, err := config.Load(fakeEnv(map[string]string{
		"IDP_WORKER_TOKEN": "a-worker-token",
	}))
	if err == nil {
		t.Fatal("Load succeeded with no IDP_CONTROL_PLANE_URL")
	}
}

func TestLoad_MissingToken(t *testing.T) {
	_, err := config.Load(fakeEnv(map[string]string{
		"IDP_CONTROL_PLANE_URL": "http://localhost:8080",
	}))
	if err == nil {
		t.Fatal("Load succeeded with neither IDP_WORKER_TOKEN nor IDP_WORKER_TOKEN_FILE set")
	}
}

func TestLoad_BothTokenSourcesSet(t *testing.T) {
	_, err := config.Load(fakeEnv(map[string]string{
		"IDP_CONTROL_PLANE_URL": "http://localhost:8080",
		"IDP_WORKER_TOKEN":      "a-worker-token",
		"IDP_WORKER_TOKEN_FILE": filepath.Join(t.TempDir(), "token"),
	}))
	if err == nil {
		t.Fatal("Load succeeded with both IDP_WORKER_TOKEN and IDP_WORKER_TOKEN_FILE set")
	}
}

func TestLoad_CustomPollInterval(t *testing.T) {
	cfg, err := config.Load(fakeEnv(map[string]string{
		"IDP_CONTROL_PLANE_URL": "http://localhost:8080",
		"IDP_WORKER_TOKEN":      "a-worker-token",
		"IDP_POLL_INTERVAL":     "30s",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want 30s", cfg.PollInterval)
	}
}
