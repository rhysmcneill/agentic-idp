// Package config loads the worker's runtime configuration from its
// environment. The worker is a long-lived process a customer's own
// deployment tooling manages, so it takes a token via IDP_WORKER_TOKEN or
// IDP_WORKER_TOKEN_FILE rather than a CLI flag — the latter works equally
// whether the customer injects it as a plain env var, a Docker secret, or a
// Kubernetes Secret mounted as a file, without this code favouring one
// mechanism over another.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const defaultPollInterval = 5 * time.Second

// Config is the worker's runtime configuration. Exactly one of Token or
// (BootstrapToken, WorkerName) is set — see Load.
type Config struct {
	ControlPlaneURL string
	Token           string
	BootstrapToken  string
	WorkerName      string
	PollInterval    time.Duration
}

// Bootstrap reports whether cfg should self-register (Decision 022) rather
// than use a pre-minted Token.
func (cfg Config) Bootstrap() bool { return cfg.BootstrapToken != "" }

// Load reads Config via getenv (rather than calling os.Getenv itself), so it
// can be exercised in a test with a fake environment:
//   - IDP_CONTROL_PLANE_URL (required)
//   - either IDP_WORKER_TOKEN/_FILE (a pre-minted credential), or
//     IDP_WORKER_BOOTSTRAP_TOKEN/_FILE plus IDP_WORKER_NAME (self-registers
//     against the control plane's shared bootstrap secret instead)
//   - IDP_POLL_INTERVAL (optional, defaults to 5s)
func Load(getenv func(string) string) (Config, error) {
	url, err := requireEnv(getenv, "IDP_CONTROL_PLANE_URL")
	if err != nil {
		return Config{}, err
	}

	token, bootstrapToken, workerName, err := loadCredentials(getenv)
	if err != nil {
		return Config{}, err
	}

	interval := defaultPollInterval
	if raw := getenv("IDP_POLL_INTERVAL"); raw != "" {
		interval, err = time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: parsing IDP_POLL_INTERVAL: %w", err)
		}
	}

	return Config{
		ControlPlaneURL: strings.TrimRight(url, "/"),
		Token:           token,
		BootstrapToken:  bootstrapToken,
		WorkerName:      workerName,
		PollInterval:    interval,
	}, nil
}

func requireEnv(getenv func(string) string, name string) (string, error) {
	v := getenv(name)
	if v == "" {
		return "", fmt.Errorf("config: %s is required", name)
	}
	return v, nil
}

// loadCredentials resolves the worker's control-plane credential: a
// pre-minted token, or bootstrap inputs to self-register with instead.
// Exactly one mode's env vars may be set.
func loadCredentials(getenv func(string) string) (token, bootstrapToken, workerName string, err error) {
	tokenSet := getenv("IDP_WORKER_TOKEN") != "" || getenv("IDP_WORKER_TOKEN_FILE") != ""
	bootstrapSet := getenv("IDP_WORKER_BOOTSTRAP_TOKEN") != "" || getenv("IDP_WORKER_BOOTSTRAP_TOKEN_FILE") != ""

	switch {
	case tokenSet && bootstrapSet:
		return "", "", "", errors.New("config: set either IDP_WORKER_TOKEN/_FILE or IDP_WORKER_BOOTSTRAP_TOKEN/_FILE, not both")
	case tokenSet:
		token, err = loadSecret(getenv, "IDP_WORKER_TOKEN", "IDP_WORKER_TOKEN_FILE")
		return token, "", "", err
	case bootstrapSet:
		bootstrapToken, err = loadSecret(getenv, "IDP_WORKER_BOOTSTRAP_TOKEN", "IDP_WORKER_BOOTSTRAP_TOKEN_FILE")
		if err != nil {
			return "", "", "", err
		}
		workerName, err = requireEnv(getenv, "IDP_WORKER_NAME")
		return "", bootstrapToken, workerName, err
	default:
		return "", "", "", errors.New("config: one of IDP_WORKER_TOKEN/_FILE or IDP_WORKER_BOOTSTRAP_TOKEN/_FILE is required")
	}
}

// loadSecret reads envName directly or fileName's contents — exactly one
// must be set. Shared by the token and bootstrap-token credential kinds.
func loadSecret(getenv func(string) string, envName, fileName string) (string, error) {
	fromEnv := getenv(envName)
	fromFile := getenv(fileName)

	switch {
	case fromEnv != "" && fromFile != "":
		return "", fmt.Errorf("config: set only one of %s or %s, not both", envName, fileName)
	case fromEnv != "":
		return fromEnv, nil
	case fromFile != "":
		b, err := os.ReadFile(fromFile) // #nosec G304 G703 -- operator-controlled path from its own deployment config, not untrusted input
		if err != nil {
			return "", fmt.Errorf("config: reading %s: %w", fileName, err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return "", fmt.Errorf("config: one of %s or %s is required", envName, fileName)
	}
}
