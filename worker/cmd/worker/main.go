// Command worker is the customer-deployed binary that polls the control
// plane, assumes tier-scoped IAM roles via broker/aws, and reports results
// back. It is the only component in this repository permitted to hold cloud
// credentials, and only in memory for the life of one request.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/rhysmcneill/agentic-idp/worker/internal/service"
)

func main() {
	if err := service.Run(context.Background(), os.Getenv); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}
