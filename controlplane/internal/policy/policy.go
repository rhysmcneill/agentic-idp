// Package policy is the single trust-tier gate called both when a run is
// created and again immediately before a queued run's payload reaches a
// worker, so a credential mint is never authorised by stored state alone.
package policy

import "github.com/rhysmcneill/agentic-idp/pkg/identity"

// Decision is the outcome of a policy check.
type Decision string

// The possible outcomes of Check.
const (
	Allow            Decision = "allow"
	RequiresApproval Decision = "requires_approval"
	Deny             Decision = "deny"
)

// Check decides whether an action at tier, mutating or not, may proceed.
// An invalid tier fails closed to Deny rather than defaulting to allow.
func Check(tier identity.Tier, mutating bool) Decision {
	if !tier.Valid() {
		return Deny
	}

	switch tier {
	case identity.TierReadOnly:
		if mutating {
			return Deny
		}
		return Allow
	case identity.TierHumanInTheLoop:
		return RequiresApproval
	case identity.TierAutonomous:
		return Allow
	default:
		return Deny
	}
}
