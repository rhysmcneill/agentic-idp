package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

func newWorkerCmd() *cobra.Command {
	worker := &cobra.Command{
		Use:   "worker",
		Short: "Manage worker credentials",
	}
	worker.AddCommand(newWorkerEnrolCmd())
	return worker
}

func newWorkerEnrolCmd() *cobra.Command {
	var name, environments string

	cmd := &cobra.Command{
		Use:   "enrol",
		Short: "Mint a new worker credential",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkerEnrol(cmd.Context(), name, environments)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "worker name, e.g. worker-staging (required)")
	cmd.Flags().StringVar(&environments, "environments", "", "comma-separated environment names (required)")
	for _, f := range []string{"name", "environments"} {
		if err := cmd.MarkFlagRequired(f); err != nil {
			panic(fmt.Sprintf("idpctl: wiring up worker enrol flags: %v", err))
		}
	}
	return cmd
}

func runWorkerEnrol(ctx context.Context, name, environments string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.CreateWorker(ctx, cfg.Token, client.CreateWorkerRequest{
		Name:         name,
		Environments: strings.Split(environments, ","),
	})
	if err != nil {
		return fmt.Errorf("enrolling worker: %w", err)
	}

	// Unlike the admin password, this token has to be shown — it's the
	// credential the operator configures into the deployed worker process,
	// and it is never recoverable from the control plane afterward.
	fmt.Println("Worker enrolled:", resp.WorkerCredentialID)
	fmt.Println("Token:", resp.Token)
	return nil
}
