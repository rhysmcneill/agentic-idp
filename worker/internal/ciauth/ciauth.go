// Package ciauth verifies a CI job's OIDC ID token and resolves it to the
// Run that authorised it. Worker-only: minting credentials requires
// pkg/cloud.Broker, which only the worker may call.
package ciauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
)

// ErrUnknownProvider is returned when no Verifier is registered for a claimed provider.
var ErrUnknownProvider = errors.New("ciauth: unknown provider")

// Claims is what a provider's OIDC ID token normalises to. Actor is audit
// only — authorisation uses Repo/WorkflowRef/RunID/Ref alone.
type Claims struct {
	Repo        string
	WorkflowRef string
	Ref         string
	RunID       string
	Actor       string
	URL         string
}

// Verifier authenticates one CI provider's OIDC ID tokens.
type Verifier interface {
	Provider() ci.Provider
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

// Registry resolves a Provider to its Verifier.
type Registry struct {
	verifiers map[ci.Provider]Verifier
}

// NewRegistry panics on duplicate providers — a wiring mistake, not a runtime condition.
func NewRegistry(verifiers ...Verifier) *Registry {
	r := &Registry{verifiers: make(map[ci.Provider]Verifier, len(verifiers))}
	for _, v := range verifiers {
		p := v.Provider()
		if _, dup := r.verifiers[p]; dup {
			panic(fmt.Sprintf("ciauth: duplicate verifier for provider %q", p))
		}
		r.verifiers[p] = v
	}
	return r
}

// Get returns the verifier for p, or ErrUnknownProvider.
func (r *Registry) Get(p ci.Provider) (Verifier, error) {
	v, ok := r.verifiers[p]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, p)
	}
	return v, nil
}
