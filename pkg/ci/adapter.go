package ci

import (
	"context"
	"fmt"
	"sort"
)

// Adapter drives one CI system. Implementations are stateless and safe for
// concurrent use; per-Environment state arrives via Config.
type Adapter interface {
	Provider() Provider
	Capabilities() Capabilities

	// Runs at Environment registration so misconfiguration surfaces during
	// onboarding rather than mid-deploy.
	ValidateConfig(ctx context.Context, cfg Config) error

	// Implementations must inject req.Correlation into the external run, as a
	// workflow input, pipeline variable or build parameter. Without it the run
	// cannot be discovered and its OIDC callback cannot be matched to the
	// record that authorised it.
	Trigger(ctx context.Context, cfg Config, req TriggerRequest) (Handle, error)

	// Returns ErrNotFound while the run has not yet appeared. Adapters whose
	// Trigger reports an ID return h unchanged.
	Resolve(ctx context.Context, cfg Config, h Handle) (Handle, error)

	Status(ctx context.Context, cfg Config, h Handle) (RunStatus, error)

	// Pass an empty offset to read from the beginning.
	Logs(ctx context.Context, cfg Config, h Handle, offset string) (LogChunk, error)

	// Returns ErrUnsupported when Capabilities().Cancel is false.
	Cancel(ctx context.Context, cfg Config, h Handle) error
}

// Registry resolves a Provider to its Adapter.
type Registry struct {
	adapters map[Provider]Adapter
}

// NewRegistry panics on duplicate providers, which is a startup wiring mistake
// rather than a runtime condition.
func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: make(map[Provider]Adapter, len(adapters))}
	for _, a := range adapters {
		p := a.Provider()
		if _, dup := r.adapters[p]; dup {
			panic(fmt.Sprintf("ci: duplicate adapter for provider %q", p))
		}
		r.adapters[p] = a
	}
	return r
}

// Get returns the adapter for p, or ErrUnknownProvider.
func (r *Registry) Get(p Provider) (Adapter, error) {
	a, ok := r.adapters[p]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, p)
	}
	return a, nil
}

// Providers lists registered providers in a stable order.
func (r *Registry) Providers() []Provider {
	out := make([]Provider, 0, len(r.adapters))
	for p := range r.adapters {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
