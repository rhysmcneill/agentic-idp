// Package service wires up and runs the worker: config, the AWS broker, and
// the poll loop. cmd/worker/main.go is a thin entrypoint over Run, kept
// separate so this is importable — and testable with a fake environment —
// without building the binary.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rhysmcneill/agentic-idp/worker/internal/broker/aws"
	"github.com/rhysmcneill/agentic-idp/worker/internal/config"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
	"github.com/rhysmcneill/agentic-idp/worker/internal/poller"
)

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

	cp := controlplane.New(cfg.ControlPlaneURL, cfg.Token)

	slog.Info("worker starting", "control_plane", cfg.ControlPlaneURL, "poll_interval", cfg.PollInterval)
	poller.Run(ctx, cp, broker, cfg.PollInterval)
	slog.Info("worker stopped")
	return nil
}
