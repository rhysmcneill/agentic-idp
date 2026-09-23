package cloud

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Redaction is a security property: a Secret must not reach logs or audit
// records through fmt or encoding/json.
func TestSecretIsRedacted(t *testing.T) {
	s := Secret("AKIAREALACCESSKEYVALUE")

	if got := s.String(); strings.Contains(got, "REALACCESSKEY") {
		t.Errorf("String() leaked the secret: %q", got)
	}

	b, err := json.Marshal(struct{ Key Secret }{s})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), "REALACCESSKEY") {
		t.Errorf("MarshalJSON leaked the secret: %s", b)
	}

	if s.Reveal() != "AKIAREALACCESSKEYVALUE" { // pragma: allowlist secret
		t.Error("Reveal did not return the underlying value")
	}
}

func TestEnvironmentConfigRequire(t *testing.T) {
	cfg := EnvironmentConfig{Settings: map[string]string{"account_ref": "123456789012"}}

	if err := cfg.Require("account_ref"); err != nil {
		t.Errorf("present setting reported missing: %v", err)
	}
	if err := cfg.Require("account_ref", "external_id"); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("got %v, want ErrInvalidConfig", err)
	}
}

func TestEnvironmentConfigGet(t *testing.T) {
	cfg := EnvironmentConfig{Settings: map[string]string{"account_ref": "123456789012"}}

	if got := cfg.Get("account_ref"); got != "123456789012" {
		t.Errorf("Get(%q) = %q, want %q", "account_ref", got, "123456789012")
	}
	if got := cfg.Get("missing"); got != "" {
		t.Errorf("Get(missing) = %q, want empty string", got)
	}
}
