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

func newPipelineCmd() *cobra.Command {
	pipeline := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage pipelines",
	}
	pipeline.AddCommand(newPipelineRegisterCmd())
	pipeline.AddCommand(newPipelineGetCmd())
	return pipeline
}

func newPipelineRegisterCmd() *cobra.Command {
	var environmentID, provider, workflowRef string
	var settings []string
	var mutating bool
	var mutatingSet bool

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a governed binding for a pipeline you already own",
		RunE: func(cmd *cobra.Command, _ []string) error {
			settingsMap, err := parseSettings(settings)
			if err != nil {
				return err
			}
			var mutatingPtr *bool
			if mutatingSet {
				mutatingPtr = &mutating
			}
			return runPipelineRegister(cmd.Context(), pipelineRegisterParams{
				environmentID: environmentID,
				provider:      provider,
				workflowRef:   workflowRef,
				settings:      settingsMap,
				mutating:      mutatingPtr,
			})
		},
	}
	cmd.Flags().StringVar(&environmentID, "environment-id", "", "environment ID this pipeline targets (required)")
	cmd.Flags().StringVar(&provider, "provider", "", "CI provider, e.g. github_actions (required)")
	cmd.Flags().StringVar(&workflowRef, "workflow-ref", "", "provider-specific workflow reference, e.g. a workflow file path (required)")
	cmd.Flags().StringArrayVar(&settings, "setting", nil, "provider-specific setting as key=value (repeatable)")
	cmd.Flags().BoolVar(&mutating, "mutating", false, "whether this pipeline can mutate the environment — only an autonomous-tier actor may set this false")
	for _, f := range []string{"environment-id", "provider", "workflow-ref"} {
		if err := cmd.MarkFlagRequired(f); err != nil {
			panic(fmt.Sprintf("idpctl: wiring up pipeline register flags: %v", err))
		}
	}
	cmd.PreRun = func(cmd *cobra.Command, _ []string) {
		mutatingSet = cmd.Flags().Changed("mutating")
	}
	return cmd
}

func parseSettings(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --setting %q: must be key=value", pair)
		}
		out[key] = value
	}
	return out, nil
}

type pipelineRegisterParams struct {
	environmentID, provider, workflowRef string
	settings                             map[string]string
	mutating                             *bool
}

func runPipelineRegister(ctx context.Context, p pipelineRegisterParams) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.CreatePipeline(ctx, cfg.Token, client.CreatePipelineRequest{
		EnvironmentID: p.environmentID,
		Provider:      p.provider,
		WorkflowRef:   p.workflowRef,
		Settings:      p.settings,
		Mutating:      p.mutating,
	})
	if err != nil {
		return fmt.Errorf("registering pipeline: %w", err)
	}

	fmt.Println("Pipeline registered:", resp.PipelineID, resp.WorkflowRef)
	return nil
}

func newPipelineGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a pipeline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPipelineGet(cmd.Context(), args[0])
		},
	}
	return cmd
}

func runPipelineGet(ctx context.Context, id string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.GetPipeline(ctx, cfg.Token, id)
	if err != nil {
		return fmt.Errorf("fetching pipeline: %w", err)
	}

	fmt.Printf("Pipeline %s\n", resp.PipelineID)
	fmt.Printf("  environment: %s\n", resp.EnvironmentID)
	fmt.Printf("  provider:    %s\n", resp.Provider)
	fmt.Printf("  workflow:    %s\n", resp.WorkflowRef)
	fmt.Printf("  mutating:    %t\n", resp.Mutating)
	return nil
}
