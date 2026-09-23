package identity_test

import (
	"errors"
	"testing"

	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestTierString(t *testing.T) {
	cases := map[identity.Tier]string{
		identity.TierReadOnly:       "read_only",
		identity.TierHumanInTheLoop: "human_in_the_loop",
		identity.TierAutonomous:     "autonomous",
	}
	for tier, want := range cases {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", tier, got, want)
		}
	}
}

func TestParseTier(t *testing.T) {
	cases := map[string]identity.Tier{
		"read_only":         identity.TierReadOnly,
		"human_in_the_loop": identity.TierHumanInTheLoop,
		"autonomous":        identity.TierAutonomous,
	}
	for name, want := range cases {
		got, err := identity.ParseTier(name)
		if err != nil {
			t.Errorf("ParseTier(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("ParseTier(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestParseTier_Unknown(t *testing.T) {
	if _, err := identity.ParseTier("super_tier"); !errors.Is(err, identity.ErrUnknownTierName) {
		t.Errorf("got %v, want ErrUnknownTierName", err)
	}
}

func TestParseTier_RoundTripsWithString(t *testing.T) {
	for _, tier := range []identity.Tier{identity.TierReadOnly, identity.TierHumanInTheLoop, identity.TierAutonomous} {
		got, err := identity.ParseTier(tier.String())
		if err != nil {
			t.Fatalf("ParseTier(%q): %v", tier.String(), err)
		}
		if got != tier {
			t.Errorf("round trip: got %d, want %d", got, tier)
		}
	}
}
