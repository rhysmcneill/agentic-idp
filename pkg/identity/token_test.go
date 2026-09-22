package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// notRevoked is a RevocationChecker fake for tests that don't exercise
// revocation itself.
type notRevoked struct{}

func (notRevoked) IsRevoked(context.Context, string, string) (bool, error) { return false, nil }

func newTestPair(t *testing.T) (*Issuer, *Verifier) {
	t.Helper()
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	return NewIssuer(priv), NewVerifier(pub)
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	issuer, verifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID:     "tenant-1",
		ActorID:      "claude-code-rhys",
		ActorType:    ActorAgent,
		Tier:         TierHumanInTheLoop,
		Environments: []string{"staging"},
		Delegation:   &Delegation{AuthorizedBy: "rhys", TeamID: "platform"},
		TTL:          time.Minute,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := verifier.Verify(context.Background(), tok, notRevoked{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.ActorID != "claude-code-rhys" || claims.TenantID != "tenant-1" {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if claims.Jti == "" {
		t.Error("Jti was not populated")
	}
	if claims.Delegation == nil || claims.Delegation.AuthorizedBy != "rhys" {
		t.Errorf("delegation not preserved: %+v", claims.Delegation)
	}
	if !claims.PermitsEnvironment("staging") || claims.PermitsEnvironment("prod") {
		t.Errorf("environment scoping wrong: %+v", claims.Environments)
	}
}

func TestIssueGeneratesDistinctJti(t *testing.T) {
	issuer, verifier := newTestPair(t)

	req := IssueRequest{TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent, Tier: TierHumanInTheLoop, TTL: time.Minute}

	tok1, err := issuer.Issue(req, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	tok2, err := issuer.Issue(req, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	c1, err := verifier.Verify(context.Background(), tok1, notRevoked{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	c2, err := verifier.Verify(context.Background(), tok2, notRevoked{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if c1.Jti == c2.Jti {
		t.Errorf("two Issue calls produced the same jti: %q", c1.Jti)
	}
}

// This is decision 005: delegation is a bound token claim. A caller cannot
// forge it by constructing a Claims value directly, because nothing in this
// package's public API accepts one — Verify is the only source.
func TestDelegationCannotBeForgedOutsideIssue(t *testing.T) {
	forged := Claims{
		ActorID:    "attacker",
		Delegation: &Delegation{AuthorizedBy: "someone-with-real-authority"},
	}
	b, err := json.Marshal(forged)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, verifier := newTestPair(t)
	// A raw, unsigned claims payload — what an attacker controlling a request
	// body would send — must not verify.
	if _, err := verifier.Verify(context.Background(), string(b), notRevoked{}); err == nil {
		t.Fatal("unsigned/forged payload verified successfully")
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	issuer, verifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierHumanInTheLoop, TTL: time.Minute,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected token shape: %d parts", len(parts))
	}
	// Flip the payload segment without re-signing.
	tampered := parts[0] + "." + parts[1] + "x" + "." + parts[2]

	if _, err := verifier.Verify(context.Background(), tampered, notRevoked{}); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("got %v, want ErrTokenInvalid", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	issuer, verifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierHumanInTheLoop, TTL: time.Nanosecond,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	if _, err := verifier.Verify(context.Background(), tok, notRevoked{}); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("got %v, want ErrTokenExpired", err)
	}
}

// An actor cannot grant a tier higher than its own, or recursive delegation
// could launder authority an actor never actually held.
func TestIssueRejectsPrivilegeEscalation(t *testing.T) {
	issuer, _ := newTestPair(t)

	_, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierAutonomous, TTL: time.Minute,
	}, TierHumanInTheLoop)

	if !errors.Is(err, ErrPrivilegeEscalation) {
		t.Errorf("got %v, want ErrPrivilegeEscalation", err)
	}
}

// The specific case the tier ordering exists to prevent: an actor that itself
// always needs a human to check its actions must not be able to mint one that
// needs no check at all — that would launder supervised authority into
// unsupervised authority. See the Tier doc comment in types.go.
func TestSupervisedActorCannotMintAutonomousAgent(t *testing.T) {
	issuer, _ := newTestPair(t)

	_, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "escalated-agent", ActorType: ActorAgent,
		Tier: TierAutonomous, TTL: time.Minute,
	}, TierHumanInTheLoop)

	if !errors.Is(err, ErrPrivilegeEscalation) {
		t.Errorf("got %v, want ErrPrivilegeEscalation", err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	issuer, _ := newTestPair(t)
	_, otherVerifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierHumanInTheLoop, TTL: time.Minute,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := otherVerifier.Verify(context.Background(), tok, notRevoked{}); err == nil {
		t.Fatal("token verified against the wrong key pair")
	}
}

func TestIssueValidatesRequest(t *testing.T) {
	issuer, _ := newTestPair(t)

	cases := []struct {
		name string
		req  IssueRequest
	}{
		{"missing tenant", IssueRequest{ActorID: "a1", Tier: TierHumanInTheLoop, TTL: time.Minute}},
		{"missing actor", IssueRequest{TenantID: "t1", Tier: TierHumanInTheLoop, TTL: time.Minute}},
		{"invalid tier", IssueRequest{TenantID: "t1", ActorID: "a1", Tier: 99, TTL: time.Minute}},
		{"zero ttl", IssueRequest{TenantID: "t1", ActorID: "a1", Tier: TierHumanInTheLoop}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := issuer.Issue(tc.req, TierAutonomous); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("got %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestVerifyRequiresRevocationChecker(t *testing.T) {
	issuer, verifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierHumanInTheLoop, TTL: time.Minute,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := verifier.Verify(context.Background(), tok, nil); err == nil {
		t.Fatal("Verify with a nil RevocationChecker: want error, got nil")
	}
}

// fakeChecker lets a test control IsRevoked's return value directly, unlike
// notRevoked which is fixed to "never revoked".
type fakeChecker struct {
	revoked bool
	err     error
}

func (f fakeChecker) IsRevoked(context.Context, string, string) (bool, error) {
	return f.revoked, f.err
}

func TestVerifyRejectsRevokedSession(t *testing.T) {
	issuer, verifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierHumanInTheLoop, TTL: time.Minute,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, err := verifier.Verify(context.Background(), tok, fakeChecker{revoked: true}); !errors.Is(err, ErrTokenRevoked) {
		t.Errorf("got %v, want ErrTokenRevoked", err)
	}
}

func TestVerifyPropagatesRevocationCheckerError(t *testing.T) {
	issuer, verifier := newTestPair(t)

	tok, err := issuer.Issue(IssueRequest{
		TenantID: "tenant-1", ActorID: "a1", ActorType: ActorAgent,
		Tier: TierHumanInTheLoop, TTL: time.Minute,
	}, TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	checkerErr := errors.New("database is down")
	if _, err := verifier.Verify(context.Background(), tok, fakeChecker{err: checkerErr}); !errors.Is(err, checkerErr) {
		t.Errorf("got %v, want it to wrap %v", err, checkerErr)
	}
}
