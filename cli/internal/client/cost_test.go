package client_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
)

func TestClient_GetRunCost(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/runs/run-1/cost" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		amount := int64(5000)
		writeJSON(t, w, http.StatusOK, []client.RunCost{
			{Source: "ci_duration", AmountMicros: &amount, Currency: "USD", CapturedAt: "2026-01-01T00:00:00Z"},
		})
	})

	got, err := client.New(ts.URL).GetRunCost(t.Context(), "tok", "run-1")
	if err != nil {
		t.Fatalf("GetRunCost: %v", err)
	}
	if len(got) != 1 || got[0].Source != "ci_duration" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestClient_GetCostsByActor_EncodesQueryParams(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("actor_id") != "actor-1" {
			t.Errorf("actor_id = %q, want actor-1", r.URL.Query().Get("actor_id"))
		}
		if r.URL.Query().Get("from") != "2026-01-01T00:00:00Z" {
			t.Errorf("from = %q", r.URL.Query().Get("from"))
		}
		writeJSON(t, w, http.StatusOK, []client.ActorCostSummary{})
	})

	_, err := client.New(ts.URL).GetCostsByActor(t.Context(), "tok", client.GetCostsByActorParams{
		ActorID: "actor-1",
		From:    "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("GetCostsByActor: %v", err)
	}
}

func TestClient_SetCostRate(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v1/cost-rates" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var got client.SetCostRateRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if got.RateMicrosPerMS != 100 {
			t.Errorf("rate_micros_per_ms = %d, want 100", got.RateMicrosPerMS)
		}
		w.WriteHeader(http.StatusOK)
	})

	err := client.New(ts.URL).SetCostRate(t.Context(), "tok", client.SetCostRateRequest{
		RateMicrosPerMS: 100,
		Currency:        "USD",
	})
	if err != nil {
		t.Fatalf("SetCostRate: %v", err)
	}
}

func TestClient_SetTelemetry(t *testing.T) {
	ts := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/tenants/telemetry" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var got struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		if !got.Enabled {
			t.Error("enabled = false, want true")
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := client.New(ts.URL).SetTelemetry(t.Context(), "tok", true); err != nil {
		t.Fatalf("SetTelemetry: %v", err)
	}
}
