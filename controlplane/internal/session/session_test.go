package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/session"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

func TestStore_IsRevoked_NeitherRevoked(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorAgent, Name: "claude-code-rhys",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	store := session.NewStore(conn)
	revoked, err := store.IsRevoked(ctx, ag.ID.String(), uuid.New().String())
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if revoked {
		t.Error("IsRevoked = true, want false for an active actor and a never-revoked session")
	}
}

func TestStore_IsRevoked_SessionRevoked(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorAgent, Name: "claude-code-rhys",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	store := session.NewStore(conn)
	jti := uuid.New()
	if _, err := store.Revoke(ctx, ag.ID, jti, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	revoked, err := store.IsRevoked(ctx, ag.ID.String(), jti.String())
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Error("IsRevoked = false, want true for a revoked session")
	}

	// A different, never-revoked session for the same actor is unaffected.
	otherRevoked, err := store.IsRevoked(ctx, ag.ID.String(), uuid.New().String())
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if otherRevoked {
		t.Error("IsRevoked = true for an unrelated session, want false")
	}
}

func TestStore_IsRevoked_WholeActorRevoked(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorAgent, Name: "claude-code-rhys",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	// Directly flip the actor's status — actor.Store has no Revoke method yet
	// (that's the enrolment/revocation API work, not yet built), so exercise
	// the schema-level behaviour session.IsRevoked depends on directly.
	if _, err := conn.ExecContext(ctx, `UPDATE actors SET status = 'revoked' WHERE id = $1`, ag.ID); err != nil {
		t.Fatalf("revoking actor directly: %v", err)
	}

	store := session.NewStore(conn)
	// A session that was never individually revoked is still rejected,
	// because the whole actor is revoked.
	revoked, err := store.IsRevoked(ctx, ag.ID.String(), uuid.New().String())
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Error("IsRevoked = false, want true when the actor itself is revoked")
	}
}

func TestStore_IsRevoked_InvalidIDs(t *testing.T) {
	conn := dbtest.New(t)
	store := session.NewStore(conn)

	if _, err := store.IsRevoked(context.Background(), "not-a-uuid", uuid.New().String()); err == nil {
		t.Error("IsRevoked with an invalid actor id: want error, got nil")
	}
	if _, err := store.IsRevoked(context.Background(), uuid.New().String(), "not-a-uuid"); err == nil {
		t.Error("IsRevoked with an invalid jti: want error, got nil")
	}
}

func TestStore_DeleteExpired(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorAgent, Name: "claude-code-rhys",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	store := session.NewStore(conn)
	expiredJti := uuid.New()
	if _, err := store.Revoke(ctx, ag.ID, expiredJti, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("Revoke (expired): %v", err)
	}
	liveJti := uuid.New()
	if _, err := store.Revoke(ctx, ag.ID, liveJti, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke (live): %v", err)
	}

	n, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteExpired removed %d rows, want 1", n)
	}

	stillRevoked, err := store.IsRevoked(ctx, ag.ID.String(), liveJti.String())
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !stillRevoked {
		t.Error("DeleteExpired removed a non-expired revocation")
	}

	expiredGone, err := store.IsRevoked(ctx, ag.ID.String(), expiredJti.String())
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if expiredGone {
		t.Error("expired revocation row was not pruned")
	}
}

func TestStore_Revoke_RequiresIDs(t *testing.T) {
	conn := dbtest.New(t)
	store := session.NewStore(conn)

	if _, err := store.Revoke(context.Background(), uuid.Nil, uuid.New(), time.Now()); err == nil {
		t.Error("Revoke with a nil actor id: want error, got nil")
	}
	if _, err := store.Revoke(context.Background(), uuid.New(), uuid.Nil, time.Now()); err == nil {
		t.Error("Revoke with a nil jti: want error, got nil")
	}
}

// End-to-end: a real identity.Verifier, backed by this Postgres-backed
// RevocationChecker, actually rejects a revoked session's still-valid token.
func TestStore_ImplementsRevocationChecker_EndToEnd(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	tn, err := tenant.NewStore(conn).Create(ctx, "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	tm, err := team.NewStore(conn).Create(ctx, tn.ID, "platform")
	if err != nil {
		t.Fatalf("creating prerequisite team: %v", err)
	}
	ag, err := actor.NewStore(conn).Create(ctx, actor.CreateParams{
		TenantID: tn.ID, Type: identity.ActorAgent, Name: "claude-code-rhys",
		TeamID: tm.ID, TrustTier: identity.TierAutonomous,
	})
	if err != nil {
		t.Fatalf("creating prerequisite actor: %v", err)
	}

	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	verifier := identity.NewVerifier(pub)
	checker := session.NewStore(conn)

	tok, err := issuer.Issue(identity.IssueRequest{
		TenantID: tn.ID.String(), ActorID: ag.ID.String(), ActorType: identity.ActorAgent,
		Tier: identity.TierAutonomous, TTL: time.Hour,
	}, identity.TierAutonomous)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := verifier.Verify(ctx, tok, checker)
	if err != nil {
		t.Fatalf("Verify before revocation: %v", err)
	}

	if _, err := checker.Revoke(ctx, ag.ID, uuid.MustParse(claims.Jti), claims.ExpiresAt); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, err := verifier.Verify(ctx, tok, checker); !errors.Is(err, identity.ErrTokenRevoked) {
		t.Errorf("Verify after revocation: got %v, want ErrTokenRevoked", err)
	}
}
