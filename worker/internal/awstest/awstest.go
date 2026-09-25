// Package awstest provisions a throwaway Floci container — a local AWS
// emulator — for tests that need to exercise real IAM/STS calls rather than
// a fake. Mirrors controlplane/internal/dbtest's shape for Postgres.
package awstest

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	image  = "floci/floci:latest"
	port   = "4566/tcp"
	region = "us-east-1"

	// AccountID is Floci's default account when the access key isn't a
	// 12-digit account number — see Floci's multi-account isolation.
	AccountID = "000000000000"
)

// Environment is a running Floci container and the AWS SDK config to reach
// it — every client (iam, sts, ...) a test needs is built from Config.
type Environment struct {
	Config aws.Config
}

// New starts a throwaway Floci container and returns an AWS SDK config
// pointed at it. Requires a reachable Docker daemon; skips the test
// otherwise, the same convention as controlplane/internal/dbtest.New.
func New(t *testing.T) *Environment {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{port},
		WaitingFor:   wait.ForLog("Ready.").WithStartupTimeout(2 * time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("starting Floci testcontainer (is Docker running?): %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminating Floci testcontainer: %v", err)
		}
	})

	endpoint, err := container.PortEndpoint(ctx, port, "http")
	if err != nil {
		t.Fatalf("getting Floci endpoint: %v", err)
	}

	connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(connCtx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
		awsconfig.WithBaseEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("building AWS config for Floci: %v", err)
	}

	return &Environment{Config: cfg}
}
