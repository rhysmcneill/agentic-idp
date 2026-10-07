package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type setTelemetryRequest struct {
	Enabled bool `json:"enabled"`
}

// handlePatchTenantTelemetry toggles the caller's tenant's opt-in telemetry
// flag. Restricted to non-ReadOnly tier, same as other tenant-wide settings
// changes — a ReadOnly actor has no mutating authority of its own.
func handlePatchTenantTelemetry(tenants *tenant.Store, audits *audit.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("tenant: no claims in request context — requireAuth was not applied"))
			return
		}
		if claims.Tier == identity.TierReadOnly {
			writeError(w, http.StatusForbidden, codeForbidden, "toggling telemetry requires human_in_the_loop or autonomous tier")
			return
		}
		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("tenant: parsing tenant id from claims: %w", err))
			return
		}
		actorID, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("tenant: parsing actor id from claims: %w", err))
			return
		}

		var req setTelemetryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}

		updated, err := tenants.SetTelemetry(r.Context(), tenantID, req.Enabled)
		if errors.Is(err, tenant.ErrNotFound) {
			writeError(w, http.StatusNotFound, codeNotFound, "unknown tenant")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(r.Context(), audit.CreateParams{
			TenantID: tenantID,
			ActorID:  actorID,
			Action:   "telemetry.toggled",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, tenantTelemetryResponse{TelemetryEnabled: updated.TelemetryEnabled})
	})
}

type tenantTelemetryResponse struct {
	TelemetryEnabled bool `json:"telemetry_enabled"`
}
