package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
)

func validEnvironmentBody() map[string]any {
	return map[string]any{
		"name":         "staging",
		"provider":     "aws",
		"region":       "eu-west-2",
		"account_ref":  "123456789012",
		"external_id":  "generated-external-id",
		"trust_anchor": "arn:aws:iam::123456789012:role/worker",
		"role_arns": map[string]string{
			"read_only":         "arn:aws:iam::123456789012:role/tier1",
			"human_in_the_loop": "arn:aws:iam::123456789012:role/tier2",
			"autonomous":        "arn:aws:iam::123456789012:role/tier3",
		},
	}
}

func TestPostEnvironments_FullFlow(t *testing.T) {
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

	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", adminToken, validEnvironmentBody())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/environments: status = %d", resp.StatusCode)
	}
	created := decodeJSON[struct {
		EnvironmentID string `json:"environment_id"`
		Name          string `json:"name"`
		Provider      string `json:"provider"`
		Region        string `json:"region"`
	}](t, resp)
	if created.EnvironmentID == "" {
		t.Fatal("response had no environment_id")
	}
	if created.Name != "staging" || created.Provider != "aws" || created.Region != "eu-west-2" {
		t.Errorf("unexpected response: %+v", created)
	}

	envID, err := uuid.Parse(created.EnvironmentID)
	if err != nil {
		t.Fatalf("parsing environment id: %v", err)
	}
	store := environment.NewStore(conn)

	cfg, err := store.GetAWSConfig(context.Background(), envID)
	if err != nil {
		t.Fatalf("GetAWSConfig: %v", err)
	}
	if cfg.AccountRef != "123456789012" || cfg.ExternalID != "generated-external-id" || cfg.TrustAnchor != "arn:aws:iam::123456789012:role/worker" {
		t.Errorf("unexpected AWS config: %+v", cfg)
	}

	for tier, wantARN := range map[identity.Tier]string{
		identity.TierReadOnly:       "arn:aws:iam::123456789012:role/tier1",
		identity.TierHumanInTheLoop: "arn:aws:iam::123456789012:role/tier2",
		identity.TierAutonomous:     "arn:aws:iam::123456789012:role/tier3",
	} {
		role, err := store.GetAWSTierRole(context.Background(), envID, tier)
		if err != nil {
			t.Fatalf("GetAWSTierRole(%d): %v", tier, err)
		}
		if role.RoleARN != wantARN {
			t.Errorf("tier %d role ARN = %q, want %q", tier, role.RoleARN, wantARN)
		}
	}
}

func TestPostEnvironments_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/v1/environments", validEnvironmentBody())
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestPostEnvironments_RequiresAutonomousTier(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
		"name": "supervised-agent", "team": "default", "tier": "human_in_the_loop", "ttl_seconds": 3600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("enrolling supervised agent: status = %d", resp.StatusCode)
	}
	supervised := decodeJSON[struct {
		Token string `json:"token"`
	}](t, resp)

	resp = doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", supervised.Token, validEnvironmentBody())
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestPostEnvironments_ValidatesRequest(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing name", func(b map[string]any) { delete(b, "name") }},
		{"unsupported provider", func(b map[string]any) { b["provider"] = "gcp" }},
		{"missing region", func(b map[string]any) { delete(b, "region") }},
		{"missing external id", func(b map[string]any) { delete(b, "external_id") }},
		{"missing trust anchor", func(b map[string]any) { delete(b, "trust_anchor") }},
		{"incomplete role arns", func(b map[string]any) {
			b["role_arns"] = map[string]string{"read_only": "arn:aws:iam::123456789012:role/tier1"}
		}},
		{"unknown tier in role arns", func(b map[string]any) {
			b["role_arns"] = map[string]string{
				"read_only":         "arn:aws:iam::123456789012:role/tier1",
				"human_in_the_loop": "arn:aws:iam::123456789012:role/tier2",
				"autonomous":        "arn:aws:iam::123456789012:role/tier3",
				"super_tier":        "arn:aws:iam::123456789012:role/tier4",
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := validEnvironmentBody()
			tc.mutate(body)
			resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/environments", adminToken, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}
