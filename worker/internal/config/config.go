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

// Config is the worker's runtime configuration.
type Config struct {
	ControlPlaneURL string
	Token           string
	PollInterval    time.Duration
}

// Load reads Config via getenv (rather than calling os.Getenv itself), so it
// can be exercised in a test with a fake environment:
//   - IDP_CONTROL_PLANE_URL (required)
//   - exactly one of IDP_WORKER_TOKEN (the token itself) or
//     IDP_WORKER_TOKEN_FILE (a path to read it from)
//   - IDP_POLL_INTERVAL (optional, defaults to 5s)
func Load(getenv func(string) string) (Config, error) {
	url, err := requireEnv(getenv, "IDP_CONTROL_PLANE_URL")
	if err != nil {
		return Config{}, err
	}

	token, err := loadToken(getenv)
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

func loadToken(getenv func(string) string) (string, error) {
	fromEnv := getenv("IDP_WORKER_TOKEN")
	fromFile := getenv("IDP_WORKER_TOKEN_FILE")

	switch {
	case fromEnv != "" && fromFile != "":
		return "", errors.New("config: set only one of IDP_WORKER_TOKEN or IDP_WORKER_TOKEN_FILE, not both")
	case fromEnv != "":
		return fromEnv, nil
	case fromFile != "":
		b, err := os.ReadFile(fromFile) // #nosec G304 G703 -- operator-controlled path from its own deployment config, not untrusted input
		if err != nil {
			return "", fmt.Errorf("config: reading IDP_WORKER_TOKEN_FILE: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return "", errors.New("config: one of IDP_WORKER_TOKEN or IDP_WORKER_TOKEN_FILE is required")
	}
}
