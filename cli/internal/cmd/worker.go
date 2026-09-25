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
	worker.AddCommand(newWorkerGrantEnvironmentCmd())
	worker.AddCommand(newWorkerListCmd())
	return worker
}

func newWorkerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List worker credentials and their granted environments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkerList(cmd.Context())
		},
	}
}

func runWorkerList(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	workers, err := c.ListWorkers(ctx, cfg.Token)
	if err != nil {
		return fmt.Errorf("listing workers: %w", err)
	}

	for _, w := range workers {
		fmt.Printf("%s\t%s\t%s\n", w.WorkerCredentialID, w.Name, strings.Join(w.Environments, ","))
	}
	return nil
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

func newWorkerGrantEnvironmentCmd() *cobra.Command {
	var environments string

	cmd := &cobra.Command{
		Use:   "grant-environment <worker-credential-id>",
		Short: "Grant an already-enrolled worker credential access to more environments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkerGrantEnvironment(cmd.Context(), args[0], environments)
		},
	}
	cmd.Flags().StringVar(&environments, "environments", "", "comma-separated environment names to add (required)")
	if err := cmd.MarkFlagRequired("environments"); err != nil {
		panic(fmt.Sprintf("idpctl: wiring up worker grant-environment flags: %v", err))
	}
	return cmd
}

func runWorkerGrantEnvironment(ctx context.Context, workerCredentialID, environments string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	if err := c.GrantWorkerEnvironments(ctx, cfg.Token, workerCredentialID, client.GrantWorkerEnvironmentsRequest{
		Environments: strings.Split(environments, ","),
	}); err != nil {
		return fmt.Errorf("granting environments: %w", err)
	}

	fmt.Println("Environments granted to", workerCredentialID)
	return nil
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
