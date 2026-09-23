package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

func newAgentCmd() *cobra.Command {
	agent := &cobra.Command{
		Use:   "agent",
		Short: "Manage agent identities",
	}
	agent.AddCommand(newAgentCreateCmd())
	return agent
}

func newAgentCreateCmd() *cobra.Command {
	var name, team, tier, environments, idempotencyKey string
	var ttl time.Duration

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Enrol a new agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentCreate(cmd.Context(), name, team, tier, environments, idempotencyKey, ttl)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "agent name (required)")
	cmd.Flags().StringVar(&team, "team", "", "team the agent belongs to (required)")
	cmd.Flags().StringVar(&tier, "tier", "", "read_only, human_in_the_loop, or autonomous (required)")
	cmd.Flags().StringVar(&environments, "environments", "", "comma-separated environment names")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "retrying with the same key returns the agent an earlier call already created, instead of a duplicate")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "how long the issued token is valid, e.g. 1h (required)")
	for _, f := range []string{"name", "team", "tier", "ttl"} {
		if err := cmd.MarkFlagRequired(f); err != nil {
			panic(fmt.Sprintf("idpctl: wiring up agent create flags: %v", err))
		}
	}
	return cmd
}

func runAgentCreate(ctx context.Context, name, team, tier, environments, idempotencyKey string, ttl time.Duration) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	var envs []string
	if environments != "" {
		envs = strings.Split(environments, ",")
	}

	c := client.New(cfg.Server)
	resp, err := c.CreateAgent(ctx, cfg.Token, client.CreateAgentRequest{
		Name:           name,
		Team:           team,
		Tier:           tier,
		Environments:   envs,
		TTLSeconds:     int64(ttl.Seconds()),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return fmt.Errorf("enrolling agent: %w", err)
	}

	// Unlike the admin password, this token has to be shown — it's the
	// credential the operator hands to the agent they just enrolled.
	fmt.Println("Agent enrolled:", resp.ActorID)
	fmt.Println("Token:", resp.Token)
	return nil
}
