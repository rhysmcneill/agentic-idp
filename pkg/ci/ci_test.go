package ci

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Redaction is a security property: a Secret must not reach logs or audit
// records through fmt or encoding/json.
func TestSecretIsRedacted(t *testing.T) {
	s := Secret("ghp_realtokenvalue")

	if got := s.String(); strings.Contains(got, "realtokenvalue") {
		t.Errorf("String() leaked the secret: %q", got)
	}

	b, err := json.Marshal(struct{ Token Secret }{s})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "realtokenvalue") {
		t.Errorf("MarshalJSON leaked the secret: %s", b)
	}

	if s.Reveal() != "ghp_realtokenvalue" {
		t.Error("Reveal did not return the underlying value")
	}
}

func TestStatusTerminal(t *testing.T) {
	terminal := []Status{StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}

	for _, s := range []Status{StatusPending, StatusRunning, StatusUnknown} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestRegistryUnknownProvider(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get(ProviderGitHubActions); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("got %v, want ErrUnknownProvider", err)
	}
}

func TestConfigRequire(t *testing.T) {
	cfg := Config{Settings: map[string]string{"repo": "owner/name"}}

	if err := cfg.Require("repo"); err != nil {
		t.Errorf("present setting reported missing: %v", err)
	}
	if err := cfg.Require("repo", "workflow"); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("got %v, want ErrInvalidConfig", err)
	}
}
