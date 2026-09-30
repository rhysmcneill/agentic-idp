package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rhysmcneill/agentic-idp/cli/internal/client"
)

func newCICmd() *cobra.Command {
	ci := &cobra.Command{
		Use:   "ci",
		Short: "CI-side integration commands",
	}
	ci.AddCommand(newCIAuthCmd())
	return ci
}

func newCIAuthCmd() *cobra.Command {
	var server, format string

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Fetch governed cloud credentials for the running CI job",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCIAuth(cmd.Context(), server, format)
		},
	}
	cmd.Flags().StringVar(&server, "server", defaultServerFromEnv(), "control plane URL")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json, env, or credential-process")
	return cmd
}

func defaultServerFromEnv() string {
	if v := os.Getenv("IDP_CONTROL_PLANE_URL"); v != "" {
		return v
	}
	return defaultServer
}

// credentials is what the CI OIDC callback returns.
type credentials struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	SessionToken    string    `json:"session_token"`
	Expiration      time.Time `json:"expiration"`
}

func runCIAuth(ctx context.Context, server, format string) error {
	if _, ok := formatWriters[format]; !ok {
		return fmt.Errorf("ci auth: unknown --format %q (want json, env, or credential-process)", format)
	}

	provider, repo, err := detectCIContext()
	if err != nil {
		return err
	}

	callbackURL, err := client.New(server).GetCICallbackURL(ctx, provider, repo)
	if err != nil {
		return fmt.Errorf("ci auth: discovering callback url: %w", err)
	}

	token, err := fetchGitHubOIDCToken(ctx, callbackURL)
	if err != nil {
		return fmt.Errorf("ci auth: fetching oidc token: %w", err)
	}

	creds, err := postCallback(ctx, callbackURL, provider, token)
	if err != nil {
		return fmt.Errorf("ci auth: %w", err)
	}

	maskers.get(provider).maskSecrets(os.Stdout, creds)
	return formatWriters[format](os.Stdout, creds)
}

// detectCIContext identifies the CI platform this process is running under
// and the repo it's running for. Only GitHub Actions is supported today.
func detectCIContext() (provider, repo string, err error) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return "", "", errors.New("ci auth: no supported CI platform detected (GITHUB_ACTIONS is not set)")
	}
	repo = os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return "", "", errors.New("ci auth: GITHUB_REPOSITORY is not set")
	}
	return "github_actions", repo, nil
}

// fetchGitHubOIDCToken requests a GitHub Actions OIDC ID token scoped to
// audience, using the runner-provided request URL/token — present only when
// the job declares `permissions: id-token: write`.
func fetchGitHubOIDCToken(ctx context.Context, audience string) (string, error) {
	reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqToken == "" {
		return "", errors.New("ACTIONS_ID_TOKEN_REQUEST_URL/_TOKEN not set — add `permissions: id-token: write` to this job")
	}

	// #nosec G704 -- reqURL is GitHub's own runner-provided token endpoint
	// (ACTIONS_ID_TOKEN_REQUEST_URL), not attacker-controlled input; audience
	// is our own discovered callback URL, never user-supplied.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+"&audience="+url.QueryEscape(audience), nil)
	if err != nil {
		return "", fmt.Errorf("building oidc token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+reqToken)

	resp, err := http.DefaultClient.Do(req) // #nosec G704 -- see justification above
	if err != nil {
		return "", fmt.Errorf("requesting oidc token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("requesting oidc token: unexpected status %d", resp.StatusCode)
	}

	var wire struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return "", fmt.Errorf("decoding oidc token response: %w", err)
	}
	return wire.Value, nil
}

// postCallback presents token to the CI OIDC callback at callbackURL and
// returns the credentials it mints.
func postCallback(ctx context.Context, callbackURL, provider, token string) (credentials, error) {
	body, err := json.Marshal(struct {
		Provider string `json:"provider"`
		Token    string `json:"token"`
	}{Provider: provider, Token: token})
	if err != nil {
		return credentials{}, fmt.Errorf("marshalling callback request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return credentials{}, fmt.Errorf("building callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return credentials{}, fmt.Errorf("calling callback: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return credentials{}, fmt.Errorf("callback returned status %d", resp.StatusCode)
	}

	var creds credentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return credentials{}, fmt.Errorf("decoding callback response: %w", err)
	}
	return creds, nil
}

// formatWriters renders credentials in the shape a given --format asks for.
var formatWriters = map[string]func(w io.Writer, c credentials) error{
	"json":               writeJSON,
	"env":                writeEnv,
	"credential-process": writeCredentialProcess,
}

func writeJSON(w io.Writer, c credentials) error {
	b, err := json.MarshalIndent(c, "", "  ") // #nosec G117 -- secrets already masked by maskers.get before this runs
	if err != nil {
		return fmt.Errorf("marshalling credentials: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}
	return nil
}

func writeEnv(w io.Writer, c credentials) error {
	_, err := fmt.Fprintf(w, "export AWS_ACCESS_KEY_ID=%s\nexport AWS_SECRET_ACCESS_KEY=%s\nexport AWS_SESSION_TOKEN=%s\n",
		shellQuote(c.AccessKeyID), shellQuote(c.SecretAccessKey), shellQuote(c.SessionToken))
	if err != nil {
		return fmt.Errorf("writing credentials: %w", err)
	}
	return nil
}

// shellQuote single-quotes s for safe use in a POSIX shell — this output is
// meant to be eval'd directly, so every value needs it regardless of how
// safe the source normally looks.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// writeCredentialProcess renders the exact JSON shape AWS's SDK expects from
// a credential_process source, so idpctl ci auth can be that source
// directly in an AWS profile.
func writeCredentialProcess(w io.Writer, c credentials) error {
	out := struct {
		Version         int    `json:"Version"`
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
	}{
		Version: 1, AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey,
		SessionToken: c.SessionToken, Expiration: c.Expiration.Format(time.RFC3339),
	}
	b, err := json.Marshal(out) // #nosec G117 -- secrets already masked by maskers.get before this runs
	if err != nil {
		return fmt.Errorf("marshalling credential_process output: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return fmt.Errorf("writing credential_process output: %w", err)
	}
	return nil
}
