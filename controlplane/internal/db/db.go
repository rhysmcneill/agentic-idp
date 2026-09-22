// Package db connects to Postgres and applies migrations at startup, so a
// deployed controlplane never needs a separate manual migration step.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/rhysmcneill/agentic-idp/controlplane/migrations"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Open connects to Postgres via dsn (a "postgres://" connection string) and
// verifies the connection with a ping.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: opening connection: %w", err)
	}
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("db: pinging database: %w", err)
	}
	return conn, nil
}

// Migrate applies every pending migration in controlplane/migrations against
// conn. Safe to call on every startup: a database already at the latest
// version is a no-op.
func Migrate(conn *sql.DB) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("db: loading embedded migrations: %w", err)
	}

	driver, err := pgxmigrate.WithInstance(conn, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("db: creating migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx", driver)
	if err != nil {
		return fmt.Errorf("db: creating migrator: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: applying migrations: %w", err)
	}
	return nil
}
