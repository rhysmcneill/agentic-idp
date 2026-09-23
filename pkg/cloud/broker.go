package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// EnvironmentConfig is the per-Environment configuration a Broker validates
// and mints credentials against. Settings holds provider-specific keys
// (broker/aws documents its own required keys) rather than typed fields:
// GCP and Azure's account-level identity and per-tier binding are shaped
// nothing like AWS's, so a typed field here would make this interface only
// pretend to be provider-agnostic. Mirrors pkg/ci.Config's shape for the
// same reason.
type EnvironmentConfig struct {
	Provider Provider
	Settings map[string]string
}

// Get returns a setting, or the empty string when absent.
func (c EnvironmentConfig) Get(key string) string { return c.Settings[key] }

// Require reports the first missing setting among keys.
func (c EnvironmentConfig) Require(keys ...string) error {
	for _, k := range keys {
		if c.Settings[k] == "" {
			return fmt.Errorf("%w: missing setting %q", ErrInvalidConfig, k)
		}
	}
	return nil
}

// Secret holds a minted credential value, redacted in its String and JSON
// forms so it cannot leak into logs, audit records or error messages when
// passed through fmt or encoding/json. Same pattern as pkg/ci.Secret.
type Secret string

func (Secret) String() string { return "[REDACTED]" }

// MarshalJSON redacts the secret so it never reaches a logged or persisted
// payload.
func (Secret) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal("[REDACTED]")
	if err != nil {
		return nil, fmt.Errorf("cloud: marshalling redacted secret: %w", err)
	}
	return b, nil
}

// Reveal returns the underlying value. Call it as late as possible — right
// before handing it to the pipeline that needs it.
func (s Secret) Reveal() string { return string(s) }

// Credentials is a set of minted, short-lived, provider-native credential
// values plus their expiry. Which keys Values holds is defined by the
// concrete Broker that minted them (broker/aws documents its own), not by
// this package, for the same reason as EnvironmentConfig.Settings above.
type Credentials struct {
	Provider  Provider
	Values    map[string]Secret
	ExpiresAt time.Time
}

// Broker mints scoped, short-lived cloud credentials for one provider. Only
// worker/internal/broker/aws implements this in v1 — GCP and Azure stay
// deferred (decision 017). Never called outside the worker: the control
// plane never holds or receives cloud credentials (see CLAUDE.md's Security
// invariants).
type Broker interface {
	Provider() Provider

	// ValidateEnvironment runs at Environment registration so a
	// misconfiguration (an unassumable role, a wrong external ID) surfaces
	// during onboarding rather than mid-run.
	ValidateEnvironment(ctx context.Context, cfg EnvironmentConfig) error

	// MintCredentials looks up the role bound to tier and calls the
	// provider's native credential-issuing API. It never returns a
	// longer-lived or more privileged credential than tier's own binding
	// allows — credential-scoping failures fail closed, never falling back
	// to a broader role.
	MintCredentials(ctx context.Context, cfg EnvironmentConfig, tier identity.Tier) (Credentials, error)
}
