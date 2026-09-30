// Package service wires up and runs the worker: config, the AWS broker, and
// the poll loop. cmd/worker/main.go is a thin entrypoint over Run, kept
// separate so this is importable — and testable with a fake environment —
// without building the binary.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/ci/githubactions"
	"github.com/rhysmcneill/agentic-idp/worker/internal/broker/aws"
	"github.com/rhysmcneill/agentic-idp/worker/internal/ciauth"
	"github.com/rhysmcneill/agentic-idp/worker/internal/ciauth/githuboidc"
	"github.com/rhysmcneill/agentic-idp/worker/internal/config"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
	"github.com/rhysmcneill/agentic-idp/worker/internal/githubapp"
	"github.com/rhysmcneill/agentic-idp/worker/internal/poller"
)

// bootstrapRetryInterval paces bootstrapRetry. A fresh, not-yet-set-up
// control plane (Decision 022) is an expected startup condition, not a
// fatal error, so this retries forever rather than exiting — the same
// "never crash on a transient dependency" convention poller.Run follows.
const bootstrapRetryInterval = 5 * time.Second

// Run loads config, constructs the AWS broker from the worker's ambient
// identity, and polls the control plane until parent is cancelled
// (SIGINT/SIGTERM).
func Run(parent context.Context, getenv func(string) string) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	broker, err := aws.NewDefault(ctx)
	if err != nil {
		return fmt.Errorf("constructing AWS broker: %w", err)
	}

	token := cfg.Token
	if cfg.Bootstrap() {
		var credentialID string
		err = bootstrapRetry(ctx, bootstrapRetryInterval, func() error {
			var bootstrapErr error
			credentialID, token, bootstrapErr = controlplane.Bootstrap(ctx, cfg.ControlPlaneURL, cfg.BootstrapToken, cfg.WorkerName)
			if bootstrapErr != nil {
				return fmt.Errorf("%w", bootstrapErr)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("bootstrapping worker credential: %w", err)
		}
		slog.Info("worker bootstrapped", "worker_credential_id", credentialID)
	}
	cp := controlplane.New(cfg.ControlPlaneURL, token)

	adapter := githubactions.New()

	var resolver *githubapp.Resolver
	if cfg.GitHubAppID != "" {
		resolver, err = githubapp.New(cfg.GitHubAppID, cfg.GitHubAppKey)
		if err != nil {
			return fmt.Errorf("constructing github app credential resolver: %w", err)
		}

		verifier, err := githuboidc.New(ctx, cfg.CICallbackURL)
		if err != nil {
			return fmt.Errorf("constructing github oidc verifier: %w", err)
		}
		httpServer := &http.Server{
			Addr:              cfg.CIListenAddr,
			Handler:           ciauth.NewHandler(ciauth.NewRegistry(verifier), cp, broker, adapter, resolver),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("ci oidc callback server failed", "error", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
		}()
		slog.Info("ci oidc callback listening", "addr", cfg.CIListenAddr, "callback_url", cfg.CICallbackURL)

		if err := cp.RegisterCICallback(ctx, cfg.CICallbackURL); err != nil {
			return fmt.Errorf("registering ci callback url: %w", err)
		}
	}

	slog.Info("worker starting", "control_plane", cfg.ControlPlaneURL, "poll_interval", cfg.PollInterval)
	if resolver != nil {
		poller.Run(ctx, cp, broker, adapter, resolver, cfg.PollInterval)
	} else {
		poller.Run(ctx, cp, broker, adapter, nil, cfg.PollInterval)
	}
	slog.Info("worker stopped")
	return nil
}

// bootstrapRetry calls fn every interval until it returns nil or ctx is
// cancelled.
func bootstrapRetry(ctx context.Context, interval time.Duration, fn func() error) error {
	for {
		err := fn()
		if err == nil {
			return nil
		}
		slog.Warn("worker bootstrap not ready yet, retrying", "error", err, "retry_in", interval)

		select {
		case <-ctx.Done():
			return fmt.Errorf("bootstrap retry: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}
