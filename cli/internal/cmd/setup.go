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

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Run first-run setup on a fresh instance",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetup(cmd.Context(), server)
		},
	}
	cmd.Flags().StringVar(&server, "server", defaultServer, "control plane URL")
	return cmd
}

func runSetup(ctx context.Context, server string) error {
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

	resp, err := c.Setup(ctx, client.SetupRequest{
		TenantName:    tenantName,
		AdminUsername: adminUsername,
		AdminPassword: password,
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
