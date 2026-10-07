package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
)

func TestTick_NotOptedIn_NoSend(t *testing.T) {
	conn := dbtest.New(t)
	tenants := tenant.NewStore(conn)
	if _, err := tenants.Create(context.Background(), "acme-corp"); err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}

	var posts int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&posts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	s := NewSender(collector.URL, "test", conn, tenants)
	s.tick(context.Background())

	if got := atomic.LoadInt32(&posts); got != 0 {
		t.Errorf("expected no POST while telemetry_enabled is false, got %d", got)
	}
}

func TestTick_OptedIn_SendsOncePayloadWithExpectedFields(t *testing.T) {
	conn := dbtest.New(t)
	tenants := tenant.NewStore(conn)
	tn, err := tenants.Create(context.Background(), "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	if _, err := tenants.SetTelemetry(context.Background(), tn.ID, true); err != nil {
		t.Fatalf("opting in: %v", err)
	}

	var posts int32
	var received Payload
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decoding payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	s := NewSender(collector.URL, "v0.0.0-test", conn, tenants)
	s.tick(context.Background())

	if got := atomic.LoadInt32(&posts); got != 1 {
		t.Fatalf("expected exactly one POST, got %d", got)
	}
	if received.InstanceID == "" {
		t.Error("InstanceID is empty")
	}
	if received.ProductVersion != "v0.0.0-test" {
		t.Errorf("ProductVersion = %q, want %q", received.ProductVersion, "v0.0.0-test")
	}
}

func TestTick_SendFailure_DoesNotPanic(t *testing.T) {
	conn := dbtest.New(t)
	tenants := tenant.NewStore(conn)
	tn, err := tenants.Create(context.Background(), "acme-corp")
	if err != nil {
		t.Fatalf("creating prerequisite tenant: %v", err)
	}
	if _, err := tenants.SetTelemetry(context.Background(), tn.ID, true); err != nil {
		t.Fatalf("opting in: %v", err)
	}

	s := NewSender("http://127.0.0.1:0", "test", conn, tenants) // unreachable
	s.tick(context.Background())                                // should log and return, not panic
}
