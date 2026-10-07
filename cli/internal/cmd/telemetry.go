package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newTelemetryCmd() *cobra.Command {
	telemetry := &cobra.Command{
		Use:   "telemetry",
		Short: "Enable or disable anonymous usage telemetry",
	}
	telemetry.AddCommand(&cobra.Command{
		Use:   "enable",
		Short: "Opt in to anonymous usage telemetry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetTelemetry(cmd.Context(), true)
		},
	})
	telemetry.AddCommand(&cobra.Command{
		Use:   "disable",
		Short: "Opt out of anonymous usage telemetry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetTelemetry(cmd.Context(), false)
		},
	})
	return telemetry
}

func runSetTelemetry(ctx context.Context, enabled bool) error {
	cfg, c, err := loggedInClient()
	if err != nil {
		return err
	}

	if err := c.SetTelemetry(ctx, cfg.Token, enabled); err != nil {
		return fmt.Errorf("setting telemetry: %w", err)
	}

	state := "disabled"
	if enabled {
		state = "enabled"
	}
	fmt.Println("Telemetry", state)
	return nil
}
