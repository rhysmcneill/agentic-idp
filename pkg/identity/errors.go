package identity

import "errors"

var (
	// ErrInvalidRequest is returned by Issue when the request is malformed,
	// e.g. a missing TenantID/ActorID or an invalid Tier.
	ErrInvalidRequest = errors.New("identity: invalid issue request")

	// ErrTokenInvalid is returned by Verify when the token fails signature
	// or structural validation.
	ErrTokenInvalid = errors.New("identity: token invalid")

	// ErrTokenExpired is returned by Verify when the token's ExpiresAt has passed.
	ErrTokenExpired = errors.New("identity: token expired")

	// ErrTokenRevoked is returned by Verify when the RevocationChecker reports
	// this session, or the actor as a whole, as revoked.
	ErrTokenRevoked = errors.New("identity: token revoked")

	// ErrPrivilegeEscalation is returned by Issue when the issuing actor
	// attempts to grant a tier higher than its own. See docs/AGENT-MODEL.md:
	// an actor may never grant more authority than it holds.
	ErrPrivilegeEscalation = errors.New("identity: cannot grant a tier higher than the issuer holds")

	// ErrUnknownTierName is returned by ParseTier when given a string that
	// isn't one of the three canonical tier names.
	ErrUnknownTierName = errors.New("identity: unknown tier name")
)
