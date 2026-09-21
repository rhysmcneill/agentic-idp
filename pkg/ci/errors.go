package ci

import "errors"

var (
	// ErrUnsupported is returned by an operation the provider cannot perform.
	ErrUnsupported = errors.New("ci: operation not supported by provider")

	// ErrUnknownProvider is returned when no adapter is registered.
	ErrUnknownProvider = errors.New("ci: unknown provider")

	// ErrInvalidConfig is returned by ValidateConfig.
	ErrInvalidConfig = errors.New("ci: invalid provider configuration")

	// ErrNotFound is returned by Resolve when no run matches the correlation
	// yet. Expected shortly after Trigger; retry with backoff until a timeout.
	ErrNotFound = errors.New("ci: run not found")

	// ErrNotResolved is returned when an operation needing an external run ID
	// gets a Handle that Resolve has not completed.
	ErrNotResolved = errors.New("ci: handle is not resolved")
)
