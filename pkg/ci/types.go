// Package ci defines a provider-agnostic interface for triggering and tracking
// runs in external CI systems.
//
// Trigger and Resolve are separate because triggering is not uniformly "call an
// API, receive a run ID": GitHub Actions' workflow_dispatch returns 204 with no
// body, Jenkins returns a queue item, GitLab returns the pipeline directly.
package ci

import (
	"encoding/json"
	"fmt"
	"time"
)

// Provider identifies a CI system implementation.
type Provider string

// Supported CI providers
const (
	ProviderGitHubActions Provider = "github_actions"
	ProviderGitLabCI      Provider = "gitlab_ci"
	ProviderJenkins       Provider = "jenkins"
	ProviderAtlantis      Provider = "atlantis"
	ProviderBitbucket     Provider = "bitbucket_pipelines"
)

// Status is the normalised run state across providers.
type Status string

// Normalised run states, mapped from each provider's native status.
const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimedOut  Status = "timed_out"
	StatusUnknown   Status = "unknown"
)

// Terminal reports whether the run should no longer be polled.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

// Capabilities declares what an adapter supports.
type Capabilities struct {
	// False means Resolve must be polled before Status or Logs will work.
	TriggerReturnsID bool

	Cancel bool
	Logs   bool

	// Without OIDC the pipeline cannot obtain tier-scoped credentials and falls
	// back to whatever role it already holds, defeating tier enforcement.
	OIDCCallback bool
}

// Handle references a run in an external CI system.
type Handle struct {
	Provider   Provider `json:"provider"`
	ExternalID string   `json:"external_id,omitempty"`
	URL        string   `json:"url,omitempty"`
	Resolved   bool     `json:"resolved"`

	// Our run ID, injected into the external run. Discovers runs for providers
	// returning no ID, and matches a pipeline's OIDC callback to the record
	// that authorised it.
	Correlation string `json:"correlation"`
}

// TriggerRequest describes a run to start.
type TriggerRequest struct {
	Ref      string
	Workflow string
	Inputs   map[string]string

	Correlation string

	// Audit only. It crosses a boundary the far side could forge, so it must
	// never feed authorisation.
	ActorRef string
}

// RunStatus is a point-in-time view of an external run.
type RunStatus struct {
	Status Status

	// Provider-native status, retained because normalisation is lossy: GitHub
	// splits status and conclusion, Jenkins has UNSTABLE.
	Raw string

	StartedAt  *time.Time
	FinishedAt *time.Time
	URL        string
}

// LogChunk is an incremental read of a run's output.
type LogChunk struct {
	Data []byte

	// Opaque: GitHub uses per-job cursors, Jenkins byte offsets, GitLab ranges.
	NextOffset string

	EOF bool
}

// Secret holds a credential, redacted in its String and JSON forms so it cannot
// leak into logs or audit records.
type Secret string

func (Secret) String() string { return "[REDACTED]" }

// MarshalJSON redacts the secret so it never reaches a logged or persisted payload.
func (Secret) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal("[REDACTED]")
	if err != nil {
		return nil, fmt.Errorf("ci: marshalling redacted secret: %w", err)
	}
	return b, nil
}

// Reveal returns the underlying value. Call it as late as possible.
func (s Secret) Reveal() string { return string(s) }

// Config is the per-Environment configuration for one CI provider.
//
// Passed per call rather than held on the adapter: adapters are stateless
// singletons, while configuration belongs to an Environment and one worker may
// serve several.
type Config struct {
	Provider Provider
	Settings map[string]string

	// Resolved by the worker immediately before use; never persisted by the
	// control plane.
	Credential Secret
}

// Get returns a setting, or the empty string when absent.
func (c Config) Get(key string) string { return c.Settings[key] }

// Require reports the first missing setting among keys.
func (c Config) Require(keys ...string) error {
	for _, k := range keys {
		if c.Settings[k] == "" {
			return fmt.Errorf("%w: missing setting %q", ErrInvalidConfig, k)
		}
	}
	return nil
}
