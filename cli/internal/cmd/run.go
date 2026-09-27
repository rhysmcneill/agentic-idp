package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

func newRunCmd() *cobra.Command {
	run := &cobra.Command{
		Use:   "run",
		Short: "Request and manage runs",
	}
	run.AddCommand(newRunRequestCmd())
	run.AddCommand(newRunGetCmd())
	run.AddCommand(newRunApproveCmd())
	run.AddCommand(newRunDenyCmd())
	return run
}

func newRunRequestCmd() *cobra.Command {
	var environmentID, pipelineID, idempotencyKey, diffRef string

	cmd := &cobra.Command{
		Use:   "request",
		Short: "Request a run against a pipeline",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRunRequest(cmd.Context(), runRequestParams{
				environmentID:  environmentID,
				pipelineID:     pipelineID,
				idempotencyKey: idempotencyKey,
				diffRef:        diffRef,
			})
		},
	}
	cmd.Flags().StringVar(&environmentID, "environment-id", "", "environment ID to run against (required)")
	cmd.Flags().StringVar(&pipelineID, "pipeline-id", "", "pipeline ID to run (required)")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "caller-supplied key so a retried request returns the earlier run instead of creating a duplicate")
	cmd.Flags().StringVar(&diffRef, "diff-ref", "", "pointer to the change diff the approver should review, for a run that requires approval")
	for _, f := range []string{"environment-id", "pipeline-id"} {
		if err := cmd.MarkFlagRequired(f); err != nil {
			panic(fmt.Sprintf("idpctl: wiring up run request flags: %v", err))
		}
	}
	return cmd
}

type runRequestParams struct {
	environmentID, pipelineID, idempotencyKey, diffRef string
}

func runRunRequest(ctx context.Context, p runRequestParams) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.RequestRun(ctx, cfg.Token, client.CreateRunRequest{
		EnvironmentID:  p.environmentID,
		PipelineID:     p.pipelineID,
		IdempotencyKey: p.idempotencyKey,
		DiffRef:        p.diffRef,
	})
	if err != nil {
		return fmt.Errorf("requesting run: %w", err)
	}

	return printRun(resp)
}

func newRunGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunGet(cmd.Context(), args[0])
		},
	}
	return cmd
}

func runRunGet(ctx context.Context, id string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.GetRun(ctx, cfg.Token, id)
	if err != nil {
		return fmt.Errorf("fetching run: %w", err)
	}

	return printRun(resp)
}

func newRunApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a run awaiting approval",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunDecide(cmd.Context(), args[0], true)
		},
	}
}

func newRunDenyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deny <id>",
		Short: "Deny a run awaiting approval",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunDecide(cmd.Context(), args[0], false)
		},
	}
}

func runRunDecide(ctx context.Context, id string, approved bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.DecideRun(ctx, cfg.Token, id, approved)
	if err != nil {
		return fmt.Errorf("deciding run: %w", err)
	}

	return printRun(resp)
}

func printRun(r client.RunResponse) error {
	fmt.Printf("Run %s\n", r.RunID)
	fmt.Printf("  environment: %s\n", r.EnvironmentID)
	fmt.Printf("  pipeline:    %s\n", r.PipelineID)
	fmt.Printf("  tier:        %s\n", r.Tier)
	fmt.Printf("  status:      %s\n", r.Status)
	return nil
}
