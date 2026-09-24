// Package server wires up and runs the control plane: config, migrations on
// startup, the HTTP API, and background maintenance (session pruning).
// cmd/server/main.go is a thin entrypoint over Run, kept separate so this is
// importable — and testable with a fake environment — without building the
// binary.
package server

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/api"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/db"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/session"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/signingkey"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// sessionPruneInterval is how often expired revoked_sessions rows are
// cleaned up. Not configurable yet — the rows are inert once past their
// expiry anyway, so the exact interval isn't load-bearing.
const sessionPruneInterval = 1 * time.Hour

type config struct {
	databaseURL          string
	listenAddr           string
	workerBootstrapToken string
}

// loadConfig takes getenv explicitly (rather than calling os.Getenv itself)
// so it — and Run, below — can be exercised in a test with a fake
// environment, not just by actually setting process env vars.
func loadConfig(getenv func(string) string) (config, error) {
	var cfg config

	cfg.databaseURL = getenv("DATABASE_URL")
	if cfg.databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}

	port := getenv("LISTEN_PORT")
	if port == "" {
		port = "8080"
	}
	cfg.listenAddr = ":" + port

	token, err := loadWorkerBootstrapToken(getenv)
	if err != nil {
		return config{}, err
	}
	cfg.workerBootstrapToken = token

	return cfg, nil
}

// loadWorkerBootstrapToken reads WORKER_BOOTSTRAP_TOKEN/_FILE. Optional —
// an empty result disables worker self-registration entirely.
func loadWorkerBootstrapToken(getenv func(string) string) (string, error) {
	fromEnv := getenv("WORKER_BOOTSTRAP_TOKEN")
	fromFile := getenv("WORKER_BOOTSTRAP_TOKEN_FILE")

	switch {
	case fromEnv != "" && fromFile != "":
		return "", errors.New("set only one of WORKER_BOOTSTRAP_TOKEN or WORKER_BOOTSTRAP_TOKEN_FILE, not both")
	case fromEnv != "":
		return fromEnv, nil
	case fromFile != "":
		b, err := os.ReadFile(fromFile) // #nosec G304 -- operator-controlled path from its own deployment config
		if err != nil {
			return "", fmt.Errorf("reading WORKER_BOOTSTRAP_TOKEN_FILE: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return "", nil
	}
}

// Run loads config, connects to Postgres, applies migrations, and serves the
// HTTP API until parent is cancelled (SIGINT/SIGTERM), then shuts down
// gracefully.
func Run(parent context.Context, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := db.Open(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.Migrate(conn); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}

	// No operator-supplied key: generated once and persisted in Postgres on
	// first startup, then reloaded on every startup after — see
	// controlplane/internal/signingkey.
	priv, err := signingkey.LoadOrCreate(ctx, conn)
	if err != nil {
		return fmt.Errorf("loading signing key: %w", err)
	}
	issuer := identity.NewIssuer(priv)
	verifier := identity.NewVerifier(priv.Public().(ed25519.PublicKey))

	srv := api.NewServer(conn, issuer, verifier)
	if cfg.workerBootstrapToken != "" {
		srv.EnableWorkerBootstrap(cfg.workerBootstrapToken)
	}

	go pruneExpiredSessions(ctx, conn)

	httpServer := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.listenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down HTTP server: %w", err)
		}
		return nil
	case err := <-serveErr:
		return fmt.Errorf("serving HTTP: %w", err)
	}
}

func pruneExpiredSessions(ctx context.Context, conn *sql.DB) {
	store := session.NewStore(conn)
	ticker := time.NewTicker(sessionPruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := store.DeleteExpired(ctx)
			if err != nil {
				slog.Error("pruning expired sessions", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("pruned expired sessions", "count", n)
			}
		}
	}
}
