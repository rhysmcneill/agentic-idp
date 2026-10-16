// Command idpctl is the CLI client for the control plane API. It never
// holds cloud credentials — see docs/REPO-STRUCTURE.md.
package main

import (
	"os"

	"github.com/rhysmcneill/agentic-idp/cli/internal/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
