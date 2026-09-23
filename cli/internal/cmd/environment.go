package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

func newEnvironmentCmd() *cobra.Command {
	environment := &cobra.Command{
		Use:   "environment",
		Short: "Manage environments",
	}
	environment.AddCommand(newEnvironmentCreateCmd())
	environment.AddCommand(newEnvironmentVerifyCmd())
	return environment
}

func newEnvironmentCreateCmd() *cobra.Command {
	var name, provider, region, accountID, externalID, trustAnchor string
	var roleARNTier1, roleARNTier2, roleARNTier3 string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Register an environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEnvironmentCreate(cmd.Context(), environmentCreateParams{
				name:         name,
				provider:     provider,
				region:       region,
				accountID:    accountID,
				externalID:   externalID,
				trustAnchor:  trustAnchor,
				roleARNTier1: roleARNTier1,
				roleARNTier2: roleARNTier2,
				roleARNTier3: roleARNTier3,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "environment name, e.g. staging (required)")
	cmd.Flags().StringVar(&provider, "provider", "aws", "cloud provider — only aws is implemented in v1")
	cmd.Flags().StringVar(&region, "region", "", "cloud region, e.g. eu-west-2 (required)")
	cmd.Flags().StringVar(&accountID, "account-id", "", "AWS account ID (required)")
	cmd.Flags().StringVar(&externalID, "external-id", "", "STS external ID for the confused-deputy check (required)")
	cmd.Flags().StringVar(&trustAnchor, "trust-anchor", "", "principal/OIDC-provider ARN the worker assumes from (required)")
	cmd.Flags().StringVar(&roleARNTier1, "role-arn-tier1", "", "IAM role ARN for the read_only tier (required)")
	cmd.Flags().StringVar(&roleARNTier2, "role-arn-tier2", "", "IAM role ARN for the human_in_the_loop tier (required)")
	cmd.Flags().StringVar(&roleARNTier3, "role-arn-tier3", "", "IAM role ARN for the autonomous tier (required)")
	for _, f := range []string{"name", "region", "account-id", "external-id", "trust-anchor", "role-arn-tier1", "role-arn-tier2", "role-arn-tier3"} {
		if err := cmd.MarkFlagRequired(f); err != nil {
			panic(fmt.Sprintf("idpctl: wiring up environment create flags: %v", err))
		}
	}
	return cmd
}

type environmentCreateParams struct {
	name, provider, region                   string
	accountID, externalID, trustAnchor       string
	roleARNTier1, roleARNTier2, roleARNTier3 string
}

func runEnvironmentCreate(ctx context.Context, p environmentCreateParams) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	resp, err := c.CreateEnvironment(ctx, cfg.Token, client.CreateEnvironmentRequest{
		Name:        p.name,
		Provider:    p.provider,
		Region:      p.region,
		AccountRef:  p.accountID,
		ExternalID:  p.externalID,
		TrustAnchor: p.trustAnchor,
		RoleARNs: map[string]string{
			"read_only":         p.roleARNTier1,
			"human_in_the_loop": p.roleARNTier2,
			"autonomous":        p.roleARNTier3,
		},
	})
	if err != nil {
		return fmt.Errorf("registering environment: %w", err)
	}

	fmt.Println("Environment registered:", resp.EnvironmentID, resp.Name)
	return nil
}

const verifyPollInterval = 2 * time.Second

func newEnvironmentVerifyCmd() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "verify <name>",
		Short: "Check that a worker can assume every tier role for an environment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvironmentVerify(cmd.Context(), args[0], timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", time.Minute, "how long to wait for a worker to complete the check")
	return cmd
}

func runEnvironmentVerify(ctx context.Context, name string, timeout time.Duration) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return errors.New("not logged in — run 'idpctl setup' first")
	}

	c := client.New(cfg.Server)
	triggered, err := c.TriggerVerification(ctx, cfg.Token, name)
	if err != nil {
		return fmt.Errorf("triggering verification: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		result, err := c.GetVerification(ctx, cfg.Token, name, triggered.VerificationID)
		if err != nil {
			return fmt.Errorf("polling verification: %w", err)
		}

		if result.Status != "pending" {
			return printVerificationResult(name, result)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for a worker to claim and complete this check — is a worker running and scoped to %q?", timeout, name)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for verification: %w", ctx.Err())
		case <-time.After(verifyPollInterval):
		}
	}
}

func printVerificationResult(environmentName string, result client.GetVerificationResponse) error {
	fmt.Printf("Environment %q: %s\n", environmentName, result.Status)
	for _, tier := range []string{"read_only", "human_in_the_loop", "autonomous"} {
		r, ok := result.TierResults[tier]
		if !ok {
			continue
		}
		if r.OK {
			fmt.Printf("  %-17s ok\n", tier)
			continue
		}
		fmt.Printf("  %-17s FAILED: %s\n", tier, r.Error)
	}
	if result.Status != "succeeded" {
		return fmt.Errorf("connectivity check %s", result.Status)
	}
	return nil
}
