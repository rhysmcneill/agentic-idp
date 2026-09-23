// Package aws implements pkg/cloud.Broker for AWS. This is the only package
// in this repository permitted to call sts:AssumeRole — see
// docs/REPO-STRUCTURE.md and CLAUDE.md's Security invariants.
package aws

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// Setting keys this broker requires on cloud.EnvironmentConfig.Settings: the
// account-level AWS identity fields a caller must populate before calling
// ValidateEnvironment or MintCredentials.
const (
	SettingAccountRef  = "account_ref"
	SettingExternalID  = "external_id"
	SettingTrustAnchor = "trust_anchor"
)

// tierRoleSettings maps a trust tier to the settings key holding its role
// ARN. Names match the read_only/human_in_the_loop/autonomous tier strings
// used across the rest of the API, so there's one tier vocabulary end to end
// rather than a second naming scheme for AWS settings keys.
var tierRoleSettings = map[identity.Tier]string{
	identity.TierReadOnly:       "role_arn_read_only",
	identity.TierHumanInTheLoop: "role_arn_human_in_the_loop",
	identity.TierAutonomous:     "role_arn_autonomous",
}

// assumeRoleSessionName identifies the worker's assumed sessions in the
// customer's CloudTrail. Fixed rather than per-run: correlating a session to
// a specific governed run is done via our own audit log, not the customer's
// IAM logs.
const assumeRoleSessionName = "agentic-idp-worker"

// defaultCredentialTTL bounds how long a minted credential is valid for — a
// governed run should never need standing access. 900s is also STS's own
// minimum for AssumeRole's DurationSeconds.
const defaultCredentialTTL = 15 * time.Minute

// stsClient is the subset of the STS API this broker calls — a small
// interface at the point of use, so tests substitute a fake instead of
// hitting real AWS.
type stsClient interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// Broker mints AWS credentials by assuming the tier role bound to an
// Environment.
type Broker struct {
	sts stsClient
}

// New constructs a Broker over an STS client.
func New(client stsClient) *Broker {
	return &Broker{sts: client}
}

// NewDefault constructs a Broker using the worker's ambient AWS identity
// (environment variables, shared config, or an attached instance/task role)
// — the identity every registered Environment's trust_anchor must trust.
func NewDefault(ctx context.Context) (*Broker, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: loading default config: %w", err)
	}
	return New(sts.NewFromConfig(cfg)), nil
}

// Provider identifies this Broker as the AWS implementation.
func (b *Broker) Provider() cloud.Provider { return cloud.ProviderAWS }

// NewEnvironmentConfig builds the cloud.EnvironmentConfig this broker expects
// from the AWS account config and per-tier role ARNs the control plane
// returns for a claimed verification job — the DB-row-shaped wire data,
// keyed by tier name string, converted into the map-based settings shape
// this package's ValidateEnvironment/MintCredentials read from.
func NewEnvironmentConfig(accountRef, externalID, trustAnchor string, roleARNsByTierName map[string]string) (cloud.EnvironmentConfig, error) {
	settings := map[string]string{
		SettingAccountRef:  accountRef,
		SettingExternalID:  externalID,
		SettingTrustAnchor: trustAnchor,
	}
	for name, arn := range roleARNsByTierName {
		tier, err := identity.ParseTier(name)
		if err != nil {
			return cloud.EnvironmentConfig{}, fmt.Errorf("aws: building environment config: %w", err)
		}
		settings[tierRoleSettings[tier]] = arn
	}
	return cloud.EnvironmentConfig{Provider: cloud.ProviderAWS, Settings: settings}, nil
}

// ValidateEnvironment checks cfg carries every setting this broker needs,
// then proves each tier's role is actually assumable — the trust-policy and
// external-ID wiring on the customer's side can only be confirmed by really
// calling STS, not by inspecting the stored config.
func (b *Broker) ValidateEnvironment(ctx context.Context, cfg cloud.EnvironmentConfig) error {
	if cfg.Provider != cloud.ProviderAWS {
		return fmt.Errorf("%w: broker/aws cannot validate provider %q", cloud.ErrInvalidConfig, cfg.Provider)
	}
	if err := cfg.Require(SettingAccountRef, SettingExternalID, SettingTrustAnchor); err != nil {
		return fmt.Errorf("aws: %w", err)
	}
	for tier, key := range tierRoleSettings {
		if err := cfg.Require(key); err != nil {
			return fmt.Errorf("tier %d: %w", tier, err)
		}
	}

	for tier := range tierRoleSettings {
		if _, err := b.assumeRole(ctx, cfg, tier, defaultCredentialTTL); err != nil {
			return fmt.Errorf("tier %d: assuming role: %w", tier, err)
		}
	}
	return nil
}

// MintCredentials assumes the role bound to tier and returns short-lived AWS
// credentials scoped to it. Fails closed: any error assuming the role — an
// unknown tier, a revoked trust policy, an STS error — returns no
// credentials at all, never a fallback to a broader role.
func (b *Broker) MintCredentials(ctx context.Context, cfg cloud.EnvironmentConfig, tier identity.Tier) (cloud.Credentials, error) {
	if !tier.Valid() {
		return cloud.Credentials{}, fmt.Errorf("%w: invalid tier %d", cloud.ErrInvalidConfig, tier)
	}
	if _, ok := tierRoleSettings[tier]; !ok {
		return cloud.Credentials{}, fmt.Errorf("%w: %d", cloud.ErrUnknownTier, tier)
	}

	out, err := b.assumeRole(ctx, cfg, tier, defaultCredentialTTL)
	if err != nil {
		return cloud.Credentials{}, fmt.Errorf("assuming role for tier %d: %w", tier, err)
	}

	creds := out.Credentials
	return cloud.Credentials{
		Provider: cloud.ProviderAWS,
		Values: map[string]cloud.Secret{
			"access_key_id":     cloud.Secret(awssdk.ToString(creds.AccessKeyId)),
			"secret_access_key": cloud.Secret(awssdk.ToString(creds.SecretAccessKey)),
			"session_token":     cloud.Secret(awssdk.ToString(creds.SessionToken)),
		},
		ExpiresAt: awssdk.ToTime(creds.Expiration),
	}, nil
}

func (b *Broker) assumeRole(ctx context.Context, cfg cloud.EnvironmentConfig, tier identity.Tier, ttl time.Duration) (*sts.AssumeRoleOutput, error) {
	out, err := b.sts.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         awssdk.String(cfg.Get(tierRoleSettings[tier])),
		RoleSessionName: awssdk.String(assumeRoleSessionName),
		ExternalId:      awssdk.String(cfg.Get(SettingExternalID)),
		DurationSeconds: awssdk.Int32(int32(ttl.Seconds())),
	})
	if err != nil {
		return nil, fmt.Errorf("sts assume role: %w", err)
	}
	return out, nil
}
