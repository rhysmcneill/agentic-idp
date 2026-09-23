package cloud

import "errors"

var (
	// ErrInvalidConfig is returned by ValidateEnvironment, and by
	// EnvironmentConfig.Require when a required setting is missing.
	ErrInvalidConfig = errors.New("cloud: invalid environment configuration")

	// ErrUnknownTier is returned by MintCredentials when cfg has no role
	// bound for the requested tier.
	ErrUnknownTier = errors.New("cloud: no role bound for tier")
)
