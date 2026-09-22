package identity

import "errors"

var (
	ErrInvalidRequest = errors.New("identity: invalid issue request")
	ErrTokenInvalid   = errors.New("identity: token invalid")
	ErrTokenExpired   = errors.New("identity: token expired")

	// ErrPrivilegeEscalation is returned by Issue when the issuing actor
	// attempts to grant a tier higher than its own. See docs/AGENT-MODEL.md:
	// an actor may never grant more authority than it holds.
	ErrPrivilegeEscalation = errors.New("identity: cannot grant a tier higher than the issuer holds")
)
