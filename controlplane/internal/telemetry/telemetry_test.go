package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/telemetry"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/telemetry/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
)

func TestEmptyEndpoint_RunReturnsImmediately(t *testing.T) {
	conn := dbtest.New(t)
	sender := telemetry.NewSender("", "test", conn, tenant.NewStore(conn))

	done := make(chan struct{})
	go func() {
		sender.Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly for an empty endpoint")
	}
}

func TestLoadOrCreateInstanceID_StableAcrossCalls(t *testing.T) {
	conn := dbtest.New(t)
	q := sqlcgen.New(conn)

	first, err := telemetry.LoadOrCreateInstanceID(context.Background(), q)
	if err != nil {
		t.Fatalf("first LoadOrCreateInstanceID: %v", err)
	}
	second, err := telemetry.LoadOrCreateInstanceID(context.Background(), q)
	if err != nil {
		t.Fatalf("second LoadOrCreateInstanceID: %v", err)
	}
	if first != second {
		t.Errorf("instance id changed between calls: %q then %q", first, second)
	}
}
