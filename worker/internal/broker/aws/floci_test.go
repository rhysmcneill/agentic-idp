package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
	"github.com/rhysmcneill/agentic-idp/worker/internal/awstest"
)

const flociExternalID = "floci-integration-test-external-id"

// trustPolicy allows Floci's default account root to assume the role,
// gated by the same external ID this test's EnvironmentConfig carries — the
// confused-deputy check this broker's assumeRole always sends.
func trustPolicy(t *testing.T) string {
	t.Helper()
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]string{"AWS": fmt.Sprintf("arn:aws:iam::%s:root", awstest.AccountID)},
				"Action":    "sts:AssumeRole",
				"Condition": map[string]any{
					"StringEquals": map[string]string{"sts:ExternalId": flociExternalID},
				},
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling trust policy: %v", err)
	}
	return string(b)
}

// createRole creates a real IAM role in Floci and returns its ARN.
func createRole(t *testing.T, iamClient *iam.Client, name string) string {
	t.Helper()
	out, err := iamClient.CreateRole(context.Background(), &iam.CreateRoleInput{
		RoleName:                 &name,
		AssumeRolePolicyDocument: awsString(trustPolicy(t)),
	})
	if err != nil {
		t.Fatalf("creating IAM role %s: %v", name, err)
	}
	return *out.Role.Arn
}

func awsString(s string) *string { return &s }

// TestBroker_AgainstFloci proves the broker actually speaks correctly to a
// real (emulated) AWS API — every unit test elsewhere in this package uses a
// fake stsClient, which can't catch a wire-format mismatch. Skips if Docker
// is unreachable.
func TestBroker_AgainstFloci(t *testing.T) {
	env := awstest.New(t)
	ctx := context.Background()

	iamClient := iam.NewFromConfig(env.Config)
	roleARNs := map[string]string{
		"read_only":         createRole(t, iamClient, "agentic-idp-test-read-only"),
		"human_in_the_loop": createRole(t, iamClient, "agentic-idp-test-human-in-the-loop"),
		"autonomous":        createRole(t, iamClient, "agentic-idp-test-autonomous"),
	}

	cfg, err := NewEnvironmentConfig(awstest.AccountID, flociExternalID, "arn:aws:iam::"+awstest.AccountID+":role/test-worker", roleARNs)
	if err != nil {
		t.Fatalf("NewEnvironmentConfig: %v", err)
	}

	broker := New(sts.NewFromConfig(env.Config))

	if err := broker.ValidateEnvironment(ctx, cfg); err != nil {
		t.Fatalf("ValidateEnvironment against Floci: %v", err)
	}

	creds, err := broker.MintCredentials(ctx, cfg, identity.TierAutonomous)
	if err != nil {
		t.Fatalf("MintCredentials against Floci: %v", err)
	}
	if creds.Provider != cloud.ProviderAWS {
		t.Errorf("Provider = %q, want %q", creds.Provider, cloud.ProviderAWS)
	}
	for _, key := range []string{"access_key_id", "secret_access_key", "session_token"} {
		if creds.Values[key] == "" {
			t.Errorf("Values[%q] is empty", key)
		}
	}

	// Prove the credentials are functional, not just present — the worker
	// itself never calls another AWS API with what it mints (Decision 002),
	// it hands the credential off, so round-tripping it is the right depth.
	assumedCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(env.Config.Region),
		awsconfig.WithBaseEndpoint(*env.Config.BaseEndpoint),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.Values["access_key_id"].Reveal(),
			creds.Values["secret_access_key"].Reveal(),
			creds.Values["session_token"].Reveal(),
		)),
	)
	if err != nil {
		t.Fatalf("building AWS config from minted credentials: %v", err)
	}

	callerID, err := sts.NewFromConfig(assumedCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Fatalf("GetCallerIdentity with minted credentials: %v", err)
	}
	// Only the role name, not the session name: Floci's GetCallerIdentity
	// reports a hardcoded session name instead of the one AssumeRole was
	// actually called with — see floci-io/floci#4395.
	if !strings.Contains(*callerID.Arn, "assumed-role/agentic-idp-test-autonomous/") {
		t.Errorf("GetCallerIdentity Arn = %q, want it to reference the assumed autonomous role", *callerID.Arn)
	}
}
