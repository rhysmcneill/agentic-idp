package policy_test

import (
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/policy"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestCheck(t *testing.T) {
	cases := []struct {
		name     string
		tier     identity.Tier
		mutating bool
		want     policy.Decision
	}{
		{"read-only, non-mutating", identity.TierReadOnly, false, policy.Allow},
		{"read-only, mutating", identity.TierReadOnly, true, policy.Deny},
		{"human-in-the-loop, non-mutating", identity.TierHumanInTheLoop, false, policy.RequiresApproval},
		{"human-in-the-loop, mutating", identity.TierHumanInTheLoop, true, policy.RequiresApproval},
		{"autonomous, non-mutating", identity.TierAutonomous, false, policy.Allow},
		{"autonomous, mutating", identity.TierAutonomous, true, policy.Allow},
		{"invalid tier below range fails closed", identity.Tier(0), true, policy.Deny},
		{"invalid tier below range fails closed, non-mutating", identity.Tier(0), false, policy.Deny},
		{"invalid tier above range fails closed", identity.Tier(4), true, policy.Deny},
		{"invalid tier above range fails closed, non-mutating", identity.Tier(4), false, policy.Deny},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.Check(tc.tier, tc.mutating); got != tc.want {
				t.Errorf("Check(%v, %v) = %q, want %q", tc.tier, tc.mutating, got, tc.want)
			}
		})
	}
}
