// Package dbtest provisions a throwaway Postgres database, running the real
// migrations, for tests that need an actual database rather than a mock.
package dbtest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/db"
)

// New starts a throwaway Postgres container, applies every migration, and
// returns an open connection. The container is torn down when t finishes.
// Requires a reachable Docker daemon; skips the test otherwise.
func New(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("agentic_idp_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("starting Postgres testcontainer (is Docker running?): %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminating Postgres testcontainer: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("getting connection string: %v", err)
	}

	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := db.Open(connCtx, dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	return conn
}
