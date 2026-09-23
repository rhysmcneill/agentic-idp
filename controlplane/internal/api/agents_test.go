package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/session"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// setupAndLogin runs POST /v1/setup and returns the admin's tenant ID, actor
// ID and token, so agent-enrolment tests have an authenticated caller.
func setupAndLogin(t *testing.T, ts *httptest.Server) (tenantID, actorID, token string) {
	t.Helper()
	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/setup", map[string]string{
		"tenant_name":    "acme-corp",
		"admin_username": "admin",
		"admin_password": "correct-horse-battery-staple",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/setup: status = %d", resp.StatusCode)
	}
	setup := decodeJSON[struct {
		TenantID string `json:"tenant_id"`
		ActorID  string `json:"actor_id"`
		Token    string `json:"token"`
	}](t, resp)
	return setup.TenantID, setup.ActorID, setup.Token
}

// doAuthedJSON is doJSON with a Bearer Authorization header attached.
func doAuthedJSON(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPostAgents_FullFlow(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	verifier := identity.NewVerifier(pub)

	srv := api.NewServer(conn, issuer, verifier)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tenantIDStr, _, adminToken := setupAndLogin(t, ts)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		t.Fatalf("parsing tenant id: %v", err)
	}

	if _, err := environment.NewStore(conn).Create(context.Background(), tenantID, "staging", cloud.ProviderAWS, "us-east-1"); err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name":         "claude-code-rhys",
		"team":         "default",
		"tier":         "autonomous",
		"environments": []string{"staging"},
		"ttl_seconds":  3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/agents: status = %d", resp.StatusCode)
	}
	agent := decodeJSON[struct {
		ActorID string `json:"actor_id"`
		Token   string `json:"token"`
	}](t, resp)
	if agent.ActorID == "" || agent.Token == "" {
		t.Fatalf("incomplete response: %+v", agent)
	}

	checker := session.NewStore(conn)
	claims, err := verifier.Verify(context.Background(), agent.Token, checker)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.ActorID != agent.ActorID {
		t.Errorf("claims.ActorID = %s, want %s", claims.ActorID, agent.ActorID)
	}
	if claims.ActorType != identity.ActorAgent {
		t.Errorf("claims.ActorType = %q, want %q", claims.ActorType, identity.ActorAgent)
	}
	if claims.Tier != identity.TierAutonomous {
		t.Errorf("claims.Tier = %d, want %d", claims.Tier, identity.TierAutonomous)
	}
	if len(claims.Environments) != 1 {
		t.Errorf("claims.Environments = %v, want exactly 1", claims.Environments)
	}
	if claims.Delegation == nil || claims.Delegation.AuthorizedBy == "" {
		t.Error("claims.Delegation.AuthorizedBy was not set")
	}
}

func TestPostAgents_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/agents", map[string]any{
		"name": "claude-code-rhys", "team": "default", "tier": "autonomous", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostAgents_RejectsTierEscalation(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	srv := api.NewServer(conn, issuer, identity.NewVerifier(pub))
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tenantIDStr, adminIDStr, _ := setupAndLogin(t, ts)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		t.Fatalf("parsing tenant id: %v", err)
	}
	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		t.Fatalf("parsing admin id: %v", err)
	}
	// Phase 0 has no second-human enrolment endpoint yet (Phase 2), so a
	// HumanInTheLoop human is created directly to exercise the tier check
	// against a non-root actor.
	supervisedToken := mintDelegatedHumanToken(t, conn, issuer, tenantID, adminID, identity.TierHumanInTheLoop, nil)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", supervisedToken, map[string]any{
		"name": "escalated-agent", "team": "default", "tier": "autonomous", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPostAgents_RejectsAgentEnrollingAgent(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "some-agent", "team": "default", "tier": "autonomous", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling prerequisite agent: status = %d", resp.StatusCode)
	}
	agent := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", agent.Token, map[string]any{
		"name": "second-agent", "team": "default", "tier": "read_only", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

// mintDelegatedHumanToken creates a human actor directly (bypassing the
// enrolment API, which has no second-human flow until Phase 2's OIDC lands)
// and mints a token for it with a Delegation claim, so tests can exercise
// the tier/environment-scope checks against a non-root actor.
func mintDelegatedHumanToken(t *testing.T, conn *sql.DB, issuer *identity.Issuer, tenantID, authorizedBy uuid.UUID, tier identity.Tier, environmentIDs []uuid.UUID) string {
	t.Helper()
	ctx := context.Background()

	tm, err := team.NewStore(conn).GetByName(ctx, tenantID, "default")
	if err != nil {
		t.Fatalf("fetching default team: %v", err)
	}

	actors := actor.NewStore(conn)
	newActor, err := actors.Create(ctx, actor.CreateParams{
		TenantID: tenantID, Type: identity.ActorHuman, Name: "second-human",
		TeamID: tm.ID, TrustTier: tier, AuthorizedBy: &authorizedBy,
	})
	if err != nil {
		t.Fatalf("creating prerequisite human actor: %v", err)
	}

	envIDStrings := make([]string, len(environmentIDs))
	for i, id := range environmentIDs {
		if err := actors.GrantEnvironment(ctx, newActor.ID, id); err != nil {
			t.Fatalf("granting environment: %v", err)
		}
		envIDStrings[i] = id.String()
	}

	token, err := issuer.Issue(identity.IssueRequest{
		TenantID:     tenantID.String(),
		ActorID:      newActor.ID.String(),
		ActorType:    identity.ActorHuman,
		Tier:         tier,
		Environments: envIDStrings,
		Delegation:   &identity.Delegation{AuthorizedBy: authorizedBy.String(), TeamID: tm.ID.String()},
		TTL:          time.Hour,
	}, identity.TierAutonomous)
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	return token
}

func TestPostAgents_UnknownTeam(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "claude-code-rhys", "team": "does-not-exist", "tier": "autonomous", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestPostAgents_RootActorGrantsAnyEnvironment(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv := api.NewServer(conn, identity.NewIssuer(priv), identity.NewVerifier(pub))
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tenantIDStr, _, adminToken := setupAndLogin(t, ts)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		t.Fatalf("parsing tenant id: %v", err)
	}
	if _, err := environment.NewStore(conn).Create(context.Background(), tenantID, "prod", cloud.ProviderAWS, "us-east-1"); err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	// The root actor (created by setup, no Delegation) is granted no
	// environments of its own, yet must still be able to scope an agent to
	// any environment in the tenant.
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "prod-agent", "team": "default", "tier": "autonomous", "environments": []string{"prod"}, "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
}

func TestPostAgents_RejectsEnvironmentOutsideOwnScope(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	srv := api.NewServer(conn, issuer, identity.NewVerifier(pub))
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tenantIDStr, adminIDStr, _ := setupAndLogin(t, ts)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		t.Fatalf("parsing tenant id: %v", err)
	}
	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		t.Fatalf("parsing admin id: %v", err)
	}
	envStore := environment.NewStore(conn)
	staging, err := envStore.Create(context.Background(), tenantID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}
	if _, err := envStore.Create(context.Background(), tenantID, "prod", cloud.ProviderAWS, "us-east-1"); err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	// A human scoped only to staging enrols an agent — it must not be able
	// to grant prod, which it does not itself hold. Created directly since
	// Phase 0 has no second-human enrolment endpoint yet (Phase 2).
	stagingHumanToken := mintDelegatedHumanToken(t, conn, issuer, tenantID, adminID, identity.TierAutonomous, []uuid.UUID{staging.ID})

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", stagingHumanToken, map[string]any{
		"name": "escalated-scope-agent", "team": "default", "tier": "autonomous", "environments": []string{"prod"}, "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPostAgents_IdempotencyKey_ReplayReturnsSameActor(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	body := map[string]any{
		"name": "retried-agent", "team": "default", "tier": "autonomous",
		"ttl_seconds": 3600, "idempotency_key": "retry-key-1",
	}

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first call: status = %d", resp.StatusCode)
	}
	first := decodeJSON[struct {
		ActorID string `json:"actor_id"`
		Token   string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replayed call: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	second := decodeJSON[struct {
		ActorID string `json:"actor_id"`
		Token   string `json:"token"`
	}](t, resp)

	if second.ActorID != first.ActorID {
		t.Errorf("replay created a different actor: %s vs %s", second.ActorID, first.ActorID)
	}
	if second.Token == first.Token {
		t.Error("replay returned the literal same token — tokens are never persisted, so this should be impossible")
	}
}

func TestPostAgents_IdempotencyReplay_RejectsWhenCallerAuthorityShrank(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	srv := api.NewServer(conn, issuer, identity.NewVerifier(pub))
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tenantIDStr, adminIDStr, _ := setupAndLogin(t, ts)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		t.Fatalf("parsing tenant id: %v", err)
	}
	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		t.Fatalf("parsing admin id: %v", err)
	}
	staging, err := environment.NewStore(conn).Create(context.Background(), tenantID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating prerequisite environment: %v", err)
	}

	// A human at Autonomous, scoped to staging, enrols an Autonomous agent
	// scoped to staging.
	humanToken := mintDelegatedHumanToken(t, conn, issuer, tenantID, adminID, identity.TierAutonomous, []uuid.UUID{staging.ID})
	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", humanToken, map[string]any{
		"name": "retried-agent", "team": "default", "tier": "autonomous",
		"environments": []string{"staging"}, "ttl_seconds": 3600, "idempotency_key": "retry-key-1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first call: status = %d", resp.StatusCode)
	}

	// The same human actor ID replays the same idempotency key, but this
	// time presents a token asserting a reduced tier — simulating whatever
	// future mechanism might legitimately narrow an actor's authority after
	// creation. The replay must re-check authority, not just trust the
	// stored actor row.
	tm, err := team.NewStore(conn).GetByName(context.Background(), tenantID, "default")
	if err != nil {
		t.Fatalf("fetching default team: %v", err)
	}
	claims, err := identity.NewVerifier(pub).Verify(context.Background(), humanToken, noopRevocationChecker{})
	if err != nil {
		t.Fatalf("parsing human token to recover its actor id: %v", err)
	}
	shrunkToken, err := issuer.Issue(identity.IssueRequest{
		TenantID:  tenantID.String(),
		ActorID:   claims.ActorID,
		ActorType: identity.ActorHuman,
		Tier:      identity.TierReadOnly,
		Delegation: &identity.Delegation{
			AuthorizedBy: adminID.String(),
			TeamID:       tm.ID.String(),
		},
		TTL: time.Hour,
	}, identity.TierAutonomous)
	if err != nil {
		t.Fatalf("issuing shrunk token: %v", err)
	}

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", shrunkToken, map[string]any{
		"name": "retried-agent", "team": "default", "tier": "autonomous",
		"environments": []string{"staging"}, "ttl_seconds": 3600, "idempotency_key": "retry-key-1",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (replay must re-check the caller's current authority)", resp.StatusCode, http.StatusForbidden)
	}
}

// noopRevocationChecker treats every session as live — sufficient for a test
// that only needs Verify to decode a token this same process just issued.
type noopRevocationChecker struct{}

func (noopRevocationChecker) IsRevoked(context.Context, string, string) (bool, error) {
	return false, nil
}
