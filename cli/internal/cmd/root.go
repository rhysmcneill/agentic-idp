// Package cmd defines idpctl's command tree. cmd/idpctl/main.go is a thin
// entrypoint over NewRootCmd, kept separate so the commands are importable
// (and testable) without building the binary.
package cmd

import "github.com/spf13/cobra"

// NewRootCmd constructs the idpctl command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "idpctl",
		Short:         "CLI client for the agentic-idp control plane",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newSetupCmd())
	root.AddCommand(newAgentCmd())
	root.AddCommand(newEnvironmentCmd())
	root.AddCommand(newWorkerCmd())
	return root
}
