// Package identity issues and verifies actor tokens.
//
// The security property this package exists to provide: an agent's delegation
// — who it acts for — is a claim signed by the control plane at issue time,
// never a value the caller supplies. A verified Claims came from Issue; there
// is no path that constructs one from untrusted input. See CLAUDE.md's
// Security invariants and docs/DECISIONS.md 005.
package identity

import "time"

// ActorType distinguishes a human from an agent actor.
type ActorType string

// The two kinds of actor a token can identify.
const (
	ActorHuman ActorType = "human"
	ActorAgent ActorType = "agent"
)

// Tier is an ordered ceiling of unsupervised trust, not an environment.
// Higher means more trusted to act without a human checking each action —
// mirroring how continuous, fully autonomous deployment is a more mature,
// more trusted state than manual-approval-gated deployment, not a lesser one.
//
// This ordering is also what keeps privilege escalation meaningful: an actor
// that itself always needs a human to check its actions (TierHumanInTheLoop)
// must not be able to mint one that needs no check at all (TierAutonomous) —
// see Issue's issuerTier check and docs/AGENT-MODEL.md's ban on recursive
// delegation laundering authority.
//
// Environment membership is a separate, independent scope on the token
// (Environments), not encoded in Tier.
type Tier int

// The trust tiers, in ascending order of unsupervised authority.
const (
	TierReadOnly       Tier = 1 // no action authority
	TierHumanInTheLoop Tier = 2 // may propose; a human approves each action before execution
	TierAutonomous     Tier = 3 // acts unattended within its granted environments; monitored and revocable after the fact
)

// Valid reports whether t is one of the defined tiers.
func (t Tier) Valid() bool { return t >= TierReadOnly && t <= TierAutonomous }

// Delegation identifies who authorised an agent to act, fixed at Issue time.
type Delegation struct {
	AuthorizedBy string `json:"authorized_by"`
	TeamID       string `json:"team_id"`
}

// Claims is the verified identity of an actor making a request. It is only
// ever produced by Verify; nothing else in this package or its callers should
// construct one from unverified input.
type Claims struct {
	TenantID     string      `json:"tenant_id"`
	ActorID      string      `json:"actor_id"`
	ActorType    ActorType   `json:"actor_type"`
	Tier         Tier        `json:"tier"`
	Environments []string    `json:"environments"`
	Delegation   *Delegation `json:"delegation,omitempty"`

	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PermitsEnvironment reports whether the actor's token scopes it to env.
func (c Claims) PermitsEnvironment(env string) bool {
	for _, e := range c.Environments {
		if e == env {
			return true
		}
	}
	return false
}

// IssueRequest is the input to Issue. Delegation is set here, by whoever holds
// the signing key, and nowhere else — nothing constructs a Claims from
// caller-supplied delegation data.
type IssueRequest struct {
	TenantID     string
	ActorID      string
	ActorType    ActorType
	Tier         Tier
	Environments []string
	Delegation   *Delegation
	TTL          time.Duration
}
