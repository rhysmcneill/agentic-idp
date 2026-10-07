package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

const defaultServer = "http://localhost:8080"

func newSetupCmd() *cobra.Command {
	var server string
	var telemetry bool

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run first-run setup on a fresh instance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// telemetryFlag is nil when --telemetry wasn't passed at all, so
			// runSetup knows to prompt for it instead of silently defaulting
			// to the flag's zero value (false).
			var telemetryFlag *bool
			if cmd.Flags().Changed("telemetry") {
				telemetryFlag = &telemetry
			}
			return runSetup(cmd.Context(), server, telemetryFlag)
		},
	}
	cmd.Flags().StringVar(&server, "server", defaultServer, "control plane URL")
	cmd.Flags().BoolVar(&telemetry, "telemetry", false, "opt in to anonymous usage telemetry (aggregate counts only, sent daily if enabled). If not passed, you will be prompted.")
	return cmd
}

func runSetup(ctx context.Context, server string, telemetryFlag *bool) error {
	c := client.New(server)

	needsSetup, err := c.NeedsSetup(ctx)
	if err != nil {
		return fmt.Errorf("checking setup status: %w", err)
	}
	if !needsSetup {
		return errors.New("this instance has already been set up")
	}

	stdin := bufio.NewReader(os.Stdin)
	tenantName, err := prompt(stdin, "Tenant name: ")
	if err != nil {
		return err
	}
	adminUsername, err := prompt(stdin, "Admin username: ")
	if err != nil {
		return err
	}
	password, err := promptPassword("Admin password: ")
	if err != nil {
		return err
	}
	confirm, err := promptPassword("Confirm password: ")
	if err != nil {
		return err
	}
	if password != confirm {
		return errors.New("passwords did not match")
	}

	telemetry := false
	if telemetryFlag != nil {
		telemetry = *telemetryFlag
	} else {
		answer, err := prompt(stdin, "Enable anonymous usage telemetry? [y/N]: ")
		if err != nil {
			return err
		}
		telemetry = strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
	}

	resp, err := c.Setup(ctx, client.SetupRequest{
		TenantName:       tenantName,
		AdminUsername:    adminUsername,
		AdminPassword:    password,
		TelemetryEnabled: telemetry,
	})
	if err != nil {
		return fmt.Errorf("running setup: %w", err)
	}

	if err := config.Save(config.Config{Server: server, Token: resp.Token}); err != nil {
		return fmt.Errorf("saving session: %w", err)
	}

	fmt.Println("Setup complete. You are logged in as", adminUsername)
	return nil
}

func prompt(r *bufio.Reader, label string) (string, error) {
	fmt.Print(label)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func promptPassword(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(b), nil
}
