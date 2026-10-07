package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/cost"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type runCostResponse struct {
	Source       string `json:"source"`
	DurationMS   *int64 `json:"duration_ms,omitempty"`
	AmountMicros *int64 `json:"amount_micros,omitempty"`
	Currency     string `json:"currency"`
	CapturedAt   string `json:"captured_at"`
}

// handleGetRunCost returns every cost row captured against one run, scoped
// to the caller's tenant via the run lookup itself.
func handleGetRunCost(runs *execution.Store, costs *cost.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("cost: no claims in request context — requireAuth was not applied"))
			return
		}
		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("cost: parsing tenant id from claims: %w", err))
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "id must be a valid UUID")
			return
		}

		if _, err := runs.Get(r.Context(), tenantID, id); errors.Is(err, execution.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown run")
			return
		} else if err != nil {
			writeInternalError(w, err)
			return
		}

		rows, err := costs.GetForRun(r.Context(), id)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		resp := make([]runCostResponse, len(rows))
		for i, row := range rows {
			resp[i] = runCostResponse{
				Source:       string(row.Source),
				DurationMS:   row.DurationMS,
				AmountMicros: row.AmountMicros,
				Currency:     row.Currency,
				CapturedAt:   row.CapturedAt.Format(time.RFC3339),
			}
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

type actorCostSummaryResponse struct {
	ActorID           string `json:"actor_id"`
	TotalAmountMicros *int64 `json:"total_amount_micros,omitempty"`
	TotalDurationMS   int64  `json:"total_duration_ms"`
	RunCount          int64  `json:"run_count"`
}

// handleGetCostsByActor returns the "cost per actor" rollup — a live query
// over run_costs joined to runs, not a separately maintained aggregate.
func handleGetCostsByActor(costs *cost.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("cost: no claims in request context — requireAuth was not applied"))
			return
		}
		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("cost: parsing tenant id from claims: %w", err))
			return
		}

		q := r.URL.Query()
		var actorID *uuid.UUID
		if v := q.Get("actor_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, "actor_id must be a valid UUID")
				return
			}
			actorID = &id
		}
		from, err := parseOptionalTime(q.Get("from"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "from must be RFC3339")
			return
		}
		to, err := parseOptionalTime(q.Get("to"))
		if err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "to must be RFC3339")
			return
		}

		summaries, err := costs.SumByActor(r.Context(), tenantID, actorID, from, to)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		resp := make([]actorCostSummaryResponse, len(summaries))
		for i, s := range summaries {
			resp[i] = actorCostSummaryResponse{
				ActorID:           s.ActorID.String(),
				TotalAmountMicros: s.TotalAmountMicros,
				TotalDurationMS:   s.TotalDurationMS,
				RunCount:          s.RunCount,
			}
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

func parseOptionalTime(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("parsing time: %w", err)
	}
	return &t, nil
}

type setCostRateRequest struct {
	EnvironmentID   string `json:"environment_id,omitempty"`
	CIProvider      string `json:"ci_provider,omitempty"`
	RateMicrosPerMS int64  `json:"rate_micros_per_ms"`
	Currency        string `json:"currency"`
}

// handlePutCostRate sets the operator-declared CI-duration-to-cost rate for
// a scope. Restricted to non-ReadOnly tier, same as other config-mutating
// endpoints — a ReadOnly actor has no mutating authority of its own.
func handlePutCostRate(costs *cost.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("cost: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier == identity.TierReadOnly {
			writeError(w, http.StatusForbidden, codeForbidden, "setting a cost rate requires human_in_the_loop or autonomous tier")
			return
		}
		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("cost: parsing tenant id from claims: %w", err))
			return
		}

		var req setCostRateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.RateMicrosPerMS < 0 {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "rate_micros_per_ms must not be negative")
			return
		}
		if req.Currency == "" {
			req.Currency = "USD"
		}

		var environmentID *uuid.UUID
		if req.EnvironmentID != "" {
			id, err := uuid.Parse(req.EnvironmentID)
			if err != nil {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, "environment_id must be a valid UUID")
				return
			}
			environmentID = &id
		}
		var ciProvider *string
		if req.CIProvider != "" {
			ciProvider = &req.CIProvider
		}

		if err := costs.SetRate(r.Context(), tenantID, environmentID, ciProvider, req.RateMicrosPerMS, req.Currency); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct{}{})
	})
}
