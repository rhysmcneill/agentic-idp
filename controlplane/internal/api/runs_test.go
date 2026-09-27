package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// fakeEnqueuer satisfies execution.Enqueuer without a real River client —
// these tests exercise the state machine and API layer, not River's own
// dispatch, which controlplane/internal/execution's own tests already cover
// against a real Postgres testcontainer.
type fakeEnqueuer struct{}

func (fakeEnqueuer) Enqueue(context.Context, uuid.UUID) error { return nil }

func TestPostPipelines_CreateAndGet(t *testing.T) {
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
	env, err := environment.NewStore(conn).Create(context.Background(), tenantID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating environment: %v", err)
	}

	resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/pipelines", adminToken, map[string]any{
		"environment_id": env.ID.String(),
		"provider":       "github_actions",
		"workflow_ref":   ".github/workflows/deploy.yml",
		"mutating":       true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/pipelines: status = %d", resp.StatusCode)
	}
	created := decodeJSON[struct {
		PipelineID string `json:"pipeline_id"`
		Mutating   bool   `json:"mutating"`
	}](t, resp)
	if !created.Mutating {
		t.Error("Mutating = false, want true")
	}

	resp = doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/pipelines/"+created.PipelineID, adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/pipelines/{id}: status = %d", resp.StatusCode)
	}
}

func TestPostRuns_TierPolicyGating(t *testing.T) {
	conn := dbtest.New(t)
	pub, priv, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	issuer := identity.NewIssuer(priv)
	verifier := identity.NewVerifier(pub)

	srv := api.NewServer(conn, issuer, verifier)
	pipelines := pipeline.NewStore(conn)
	runs := execution.NewStore(conn, pipelines, fakeEnqueuer{})
	srv.EnableExecution(runs)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	tenantIDStr, _, adminToken := setupAndLogin(t, ts)
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		t.Fatalf("parsing tenant id: %v", err)
	}
	env, err := environment.NewStore(conn).Create(context.Background(), tenantID, "staging", cloud.ProviderAWS, "us-east-1")
	if err != nil {
		t.Fatalf("creating environment: %v", err)
	}

	tokenForTier := make(map[identity.Tier]string)
	for name, tier := range map[string]identity.Tier{
		"read_only":         identity.TierReadOnly,
		"human_in_the_loop": identity.TierHumanInTheLoop,
		"autonomous":        identity.TierAutonomous,
	} {
		resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/agents", adminToken, map[string]any{
			"name":         "agent-" + name,
			"team":         "default",
			"tier":         name,
			"environments": []string{"staging"},
			"ttl_seconds":  3600,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("enrolling %s agent: status = %d", name, resp.StatusCode)
		}
		agent := decodeJSON[struct {
			Token string `json:"token"`
		}](t, resp)
		tokenForTier[tier] = agent.Token
	}

	createPipelineAs := func(mutating bool) string {
		resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/pipelines", adminToken, map[string]any{
			"environment_id": env.ID.String(),
			"provider":       "github_actions",
			"workflow_ref":   ".github/workflows/deploy.yml",
			"mutating":       mutating,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /v1/pipelines: status = %d", resp.StatusCode)
		}
		return decodeJSON[struct {
			PipelineID string `json:"pipeline_id"`
		}](t, resp).PipelineID
	}
	mutatingPipeline := createPipelineAs(true)
	readOnlyPipeline := createPipelineAs(false)

	requestRun := func(token, pipelineID string) *http.Response {
		return doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/runs", token, map[string]any{
			"environment_id": env.ID.String(),
			"pipeline_id":    pipelineID,
		})
	}

	t.Run("ReadOnly mutating pipeline is denied", func(t *testing.T) {
		resp := requestRun(tokenForTier[identity.TierReadOnly], mutatingPipeline)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
		}
	})

	t.Run("ReadOnly non-mutating pipeline is auto-queued", func(t *testing.T) {
		resp := requestRun(tokenForTier[identity.TierReadOnly], readOnlyPipeline)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		run := decodeJSON[struct{ Status string }](t, resp)
		if run.Status != execution.StatusQueued {
			t.Errorf("status = %q, want %q", run.Status, execution.StatusQueued)
		}
	})

	t.Run("Autonomous mutating pipeline is auto-queued", func(t *testing.T) {
		resp := requestRun(tokenForTier[identity.TierAutonomous], mutatingPipeline)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		run := decodeJSON[struct{ Status string }](t, resp)
		if run.Status != execution.StatusQueued {
			t.Errorf("status = %q, want %q", run.Status, execution.StatusQueued)
		}
	})

	t.Run("HumanInTheLoop always awaits approval, then can be approved", func(t *testing.T) {
		resp := requestRun(tokenForTier[identity.TierHumanInTheLoop], mutatingPipeline)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		created := decodeJSON[struct {
			RunID  string `json:"run_id"`
			Status string `json:"status"`
		}](t, resp)
		if created.Status != execution.StatusAwaitingApproval {
			t.Fatalf("status = %q, want %q", created.Status, execution.StatusAwaitingApproval)
		}

		getResp := doAuthedJSON(t, http.MethodGet, ts.URL+"/v1/runs/"+created.RunID, adminToken, nil)
		if getResp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/runs/{id}: status = %d", getResp.StatusCode)
		}

		decideResp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/runs/"+created.RunID+"/decision", adminToken, map[string]string{
			"decision": "approved",
		})
		if decideResp.StatusCode != http.StatusOK {
			t.Fatalf("POST decision: status = %d", decideResp.StatusCode)
		}
		decided := decodeJSON[struct{ Status string }](t, decideResp)
		if decided.Status != execution.StatusQueued {
			t.Errorf("status after approval = %q, want %q", decided.Status, execution.StatusQueued)
		}

		t.Run("deciding again is a conflict", func(t *testing.T) {
			again := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/runs/"+created.RunID+"/decision", adminToken, map[string]string{
				"decision": "approved",
			})
			if again.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want %d", again.StatusCode, http.StatusConflict)
			}
		})
	})

	t.Run("ReadOnly actor cannot decide a run", func(t *testing.T) {
		resp := requestRun(tokenForTier[identity.TierHumanInTheLoop], mutatingPipeline)
		created := decodeJSON[struct {
			RunID string `json:"run_id"`
		}](t, resp)

		decideResp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/runs/"+created.RunID+"/decision", tokenForTier[identity.TierReadOnly], map[string]string{
			"decision": "approved",
		})
		if decideResp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", decideResp.StatusCode, http.StatusForbidden)
		}
	})

	t.Run("an actor cannot decide its own run", func(t *testing.T) {
		resp := requestRun(tokenForTier[identity.TierHumanInTheLoop], mutatingPipeline)
		created := decodeJSON[struct {
			RunID string `json:"run_id"`
		}](t, resp)

		decideResp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/runs/"+created.RunID+"/decision", tokenForTier[identity.TierHumanInTheLoop], map[string]string{
			"decision": "approved",
		})
		if decideResp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", decideResp.StatusCode, http.StatusForbidden)
		}
	})

	t.Run("HumanInTheLoop cannot mark its own pipeline non-mutating", func(t *testing.T) {
		resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/pipelines", tokenForTier[identity.TierHumanInTheLoop], map[string]any{
			"environment_id": env.ID.String(),
			"provider":       "github_actions",
			"workflow_ref":   ".github/workflows/deploy.yml",
			"mutating":       false,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}
		created := decodeJSON[struct{ Mutating bool }](t, resp)
		if !created.Mutating {
			t.Error("Mutating = false, want forced to true for a HumanInTheLoop-created pipeline")
		}
	})

	t.Run("ReadOnly cannot register a pipeline", func(t *testing.T) {
		resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/pipelines", tokenForTier[identity.TierReadOnly], map[string]any{
			"environment_id": env.ID.String(),
			"provider":       "github_actions",
			"workflow_ref":   ".github/workflows/deploy.yml",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
		}
	})

	t.Run("pipeline creation rejects an environment from another tenant", func(t *testing.T) {
		otherTenant, err := tenant.NewStore(conn).Create(context.Background(), "other-corp")
		if err != nil {
			t.Fatalf("creating other tenant: %v", err)
		}
		otherEnv, err := environment.NewStore(conn).Create(context.Background(), otherTenant.ID, "other-tenant-env", cloud.ProviderAWS, "us-east-1")
		if err != nil {
			t.Fatalf("creating other-tenant environment: %v", err)
		}
		resp := doAuthedJSON(t, http.MethodPost, ts.URL+"/v1/pipelines", adminToken, map[string]any{
			"environment_id": otherEnv.ID.String(),
			"provider":       "github_actions",
			"workflow_ref":   ".github/workflows/deploy.yml",
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}
	})
}
