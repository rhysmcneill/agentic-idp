// Command server runs the control plane: the HTTP API, migrations on
// startup, and background maintenance (session pruning).
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/server"
)

func main() {
	if err := server.Run(context.Background(), os.Getenv); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
