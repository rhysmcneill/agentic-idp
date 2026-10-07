package api_test

import (
	"net/http"
	"testing"
)

func TestPatchTenantTelemetry_TogglesFlag(t *testing.T) {
	ts := newTestServer(t)
	_, _, adminToken := setupAndLogin(t, ts)

	resp := doAuthedJSON(t, http.MethodPatch, ts.URL+"/v1/tenants/telemetry", adminToken, map[string]any{
		"enabled": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	got := decodeJSON[struct {
		TelemetryEnabled bool `json:"telemetry_enabled"`
	}](t, resp)
	if !got.TelemetryEnabled {
		t.Error("telemetry_enabled = false, want true")
	}

	resp = doAuthedJSON(t, http.MethodPatch, ts.URL+"/v1/tenants/telemetry", adminToken, map[string]any{
		"enabled": false,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	got = decodeJSON[struct {
		TelemetryEnabled bool `json:"telemetry_enabled"`
	}](t, resp)
	if got.TelemetryEnabled {
		t.Error("telemetry_enabled = true, want false")
	}
}
