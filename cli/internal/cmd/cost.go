package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

// microsPerCurrencyUnit converts the stored rate_micros_per_ms /
// amount_micros form to/from a human-facing currency amount.
const microsPerCurrencyUnit = 1_000_000

func newCostCmd() *cobra.Command {
	cost := &cobra.Command{
		Use:   "cost",
		Short: "View and configure run cost attribution",
	}
	cost.AddCommand(newCostRunCmd())
	cost.AddCommand(newCostByActorCmd())
	cost.AddCommand(newCostSetRateCmd())
	return cost
}

func newCostRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <run-id>",
		Short: "Show cost captured against a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCostRun(cmd.Context(), args[0])
		},
	}
}

func runCostRun(ctx context.Context, runID string) error {
	cfg, c, err := loggedInClient()
	if err != nil {
		return err
	}

	rows, err := c.GetRunCost(ctx, cfg.Token, runID)
	if err != nil {
		return fmt.Errorf("fetching run cost: %w", err)
	}
	if len(rows) == 0 {
		fmt.Println("No cost captured for this run yet.")
		return nil
	}
	for _, row := range rows {
		fmt.Printf("source:    %s\n", row.Source)
		if row.DurationMS != nil {
			fmt.Printf("duration:  %dms\n", *row.DurationMS)
		}
		if row.AmountMicros != nil {
			fmt.Printf("amount:    %.6f %s\n", float64(*row.AmountMicros)/microsPerCurrencyUnit, row.Currency)
		} else {
			fmt.Println("amount:    (no rate configured)")
		}
		fmt.Printf("captured:  %s\n\n", row.CapturedAt)
	}
	return nil
}

func newCostByActorCmd() *cobra.Command {
	var actorID, from, to string
	cmd := &cobra.Command{
		Use:   "by-actor",
		Short: "Show cost rolled up per actor",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCostByActor(cmd.Context(), client.GetCostsByActorParams{ActorID: actorID, From: from, To: to})
		},
	}
	cmd.Flags().StringVar(&actorID, "actor-id", "", "narrow to one actor")
	cmd.Flags().StringVar(&from, "from", "", "only runs costed on or after this time (RFC3339)")
	cmd.Flags().StringVar(&to, "to", "", "only runs costed before this time (RFC3339)")
	return cmd
}

func runCostByActor(ctx context.Context, params client.GetCostsByActorParams) error {
	cfg, c, err := loggedInClient()
	if err != nil {
		return err
	}

	summaries, err := c.GetCostsByActor(ctx, cfg.Token, params)
	if err != nil {
		return fmt.Errorf("fetching cost by actor: %w", err)
	}
	if len(summaries) == 0 {
		fmt.Println("No cost captured yet.")
		return nil
	}
	for _, s := range summaries {
		fmt.Printf("actor:     %s\n", s.ActorID)
		fmt.Printf("runs:      %d\n", s.RunCount)
		fmt.Printf("duration:  %dms\n", s.TotalDurationMS)
		if s.TotalAmountMicros != nil {
			fmt.Printf("amount:    %.6f\n", float64(*s.TotalAmountMicros)/microsPerCurrencyUnit)
		} else {
			fmt.Println("amount:    (no rate configured)")
		}
		fmt.Println()
	}
	return nil
}

func newCostSetRateCmd() *cobra.Command {
	var ratePerMinute float64
	var currency, environmentID, ciProvider string

	cmd := &cobra.Command{
		Use:   "set-rate",
		Short: "Set the cost of one minute of CI run time for a provider/environment",
		Long: "The cost of one minute of CI run time, as billed to you — look this up " +
			"from your CI provider's billing page, or calculate it from your own " +
			"self-hosted infra cost. This is a self-declared estimate for attribution, " +
			"not a reconciled bill.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCostSetRate(cmd.Context(), ratePerMinute, currency, environmentID, ciProvider)
		},
	}
	cmd.Flags().Float64Var(&ratePerMinute, "rate-per-minute", 0, "cost of one minute of CI run time, in --currency (required)")
	cmd.Flags().StringVar(&currency, "currency", "USD", "currency code")
	cmd.Flags().StringVar(&environmentID, "environment-id", "", "narrow this rate to one environment")
	cmd.Flags().StringVar(&ciProvider, "ci-provider", "", "narrow this rate to one CI provider")
	if err := cmd.MarkFlagRequired("rate-per-minute"); err != nil {
		panic(fmt.Sprintf("idpctl: wiring up cost set-rate flags: %v", err))
	}
	return cmd
}

func runCostSetRate(ctx context.Context, ratePerMinute float64, currency, environmentID, ciProvider string) error {
	cfg, c, err := loggedInClient()
	if err != nil {
		return err
	}

	rateMicrosPerMS := int64(ratePerMinute * microsPerCurrencyUnit / 60_000)
	if err := c.SetCostRate(ctx, cfg.Token, client.SetCostRateRequest{
		EnvironmentID:   environmentID,
		CIProvider:      ciProvider,
		RateMicrosPerMS: rateMicrosPerMS,
		Currency:        currency,
	}); err != nil {
		return fmt.Errorf("setting cost rate: %w", err)
	}
	fmt.Println("Cost rate set.")
	return nil
}

func loggedInClient() (config.Config, *client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, fmt.Errorf("loading session: %w", err)
	}
	if cfg.Token == "" {
		return config.Config{}, nil, errors.New("not logged in — run 'idpctl setup' first")
	}
	return cfg, client.New(cfg.Server), nil
}
