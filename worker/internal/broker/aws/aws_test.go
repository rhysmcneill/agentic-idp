package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"

	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// fakeSTS lets tests control AssumeRole's outcome without calling real AWS.
type fakeSTS struct {
	// calls records every RoleArn this fake was asked to assume, in order.
	calls []string
	// failFor, when non-empty, makes AssumeRole fail for that exact RoleArn.
	failFor string
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	roleARN := awssdk.ToString(in.RoleArn)
	f.calls = append(f.calls, roleARN)
	if f.failFor != "" && roleARN == f.failFor {
		return nil, errors.New("fake: AccessDenied assuming role")
	}
	return &sts.AssumeRoleOutput{
		Credentials: &types.Credentials{
			AccessKeyId:     awssdk.String("AKIA" + roleARN),
			SecretAccessKey: awssdk.String("secret-for-" + roleARN),
			SessionToken:    awssdk.String("token-for-" + roleARN),
			Expiration:      awssdk.Time(time.Now().Add(15 * time.Minute)),
		},
	}, nil
}

func validConfig() cloud.EnvironmentConfig {
	return cloud.EnvironmentConfig{
		Provider: cloud.ProviderAWS,
		Settings: map[string]string{
			SettingAccountRef:            "123456789012",
			SettingExternalID:            "generated-external-id",
			SettingTrustAnchor:           "arn:aws:iam::123456789012:role/worker",
			"role_arn_read_only":         "arn:aws:iam::123456789012:role/tier1",
			"role_arn_human_in_the_loop": "arn:aws:iam::123456789012:role/tier2",
			"role_arn_autonomous":        "arn:aws:iam::123456789012:role/tier3",
		},
	}
}

func TestBroker_Provider(t *testing.T) {
	b := New(&fakeSTS{})
	if b.Provider() != cloud.ProviderAWS {
		t.Errorf("Provider() = %q, want %q", b.Provider(), cloud.ProviderAWS)
	}
}

func TestValidateEnvironment_Success(t *testing.T) {
	fake := &fakeSTS{}
	b := New(fake)

	if err := b.ValidateEnvironment(context.Background(), validConfig()); err != nil {
		t.Fatalf("ValidateEnvironment: %v", err)
	}
	if len(fake.calls) != 3 {
		t.Errorf("assumed %d roles, want 3 (one per tier)", len(fake.calls))
	}
}

func TestValidateEnvironment_WrongProvider(t *testing.T) {
	b := New(&fakeSTS{})
	cfg := validConfig()
	cfg.Provider = cloud.ProviderGCP

	if err := b.ValidateEnvironment(context.Background(), cfg); !errors.Is(err, cloud.ErrInvalidConfig) {
		t.Errorf("got %v, want ErrInvalidConfig", err)
	}
}

func TestValidateEnvironment_MissingSettings(t *testing.T) {
	cases := []string{
		SettingAccountRef, SettingExternalID, SettingTrustAnchor,
		"role_arn_read_only", "role_arn_human_in_the_loop", "role_arn_autonomous",
	}
	for _, missing := range cases {
		t.Run(missing, func(t *testing.T) {
			cfg := validConfig()
			delete(cfg.Settings, missing)

			b := New(&fakeSTS{})
			if err := b.ValidateEnvironment(context.Background(), cfg); !errors.Is(err, cloud.ErrInvalidConfig) {
				t.Errorf("got %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestValidateEnvironment_RoleNotAssumable(t *testing.T) {
	fake := &fakeSTS{failFor: "arn:aws:iam::123456789012:role/tier2"}
	b := New(fake)

	if err := b.ValidateEnvironment(context.Background(), validConfig()); err == nil {
		t.Fatal("ValidateEnvironment succeeded despite one tier's role being unassumable")
	}
}

func TestMintCredentials_Success(t *testing.T) {
	fake := &fakeSTS{}
	b := New(fake)

	creds, err := b.MintCredentials(context.Background(), validConfig(), identity.TierAutonomous)
	if err != nil {
		t.Fatalf("MintCredentials: %v", err)
	}
	if creds.Provider != cloud.ProviderAWS {
		t.Errorf("Provider = %q, want %q", creds.Provider, cloud.ProviderAWS)
	}
	for _, key := range []string{"access_key_id", "secret_access_key", "session_token"} {
		if creds.Values[key] == "" {
			t.Errorf("Values[%q] is empty", key)
		}
	}
	if creds.ExpiresAt.Before(time.Now()) {
		t.Error("ExpiresAt is in the past")
	}
	if len(fake.calls) != 1 || fake.calls[0] != "arn:aws:iam::123456789012:role/tier3" {
		t.Errorf("assumed roles = %v, want exactly the autonomous tier's role", fake.calls)
	}
}

func TestMintCredentials_InvalidTier(t *testing.T) {
	b := New(&fakeSTS{})

	if _, err := b.MintCredentials(context.Background(), validConfig(), identity.Tier(99)); !errors.Is(err, cloud.ErrInvalidConfig) {
		t.Errorf("got %v, want ErrInvalidConfig", err)
	}
}

func TestMintCredentials_AssumeRoleFails(t *testing.T) {
	fake := &fakeSTS{failFor: "arn:aws:iam::123456789012:role/tier3"}
	b := New(fake)

	if _, err := b.MintCredentials(context.Background(), validConfig(), identity.TierAutonomous); err == nil {
		t.Fatal("MintCredentials succeeded despite AssumeRole failing")
	}
}

func TestNewEnvironmentConfig_MatchesValidConfig(t *testing.T) {
	got, err := NewEnvironmentConfig(
		"123456789012",
		"generated-external-id",
		"arn:aws:iam::123456789012:role/worker",
		map[string]string{
			"read_only":         "arn:aws:iam::123456789012:role/tier1",
			"human_in_the_loop": "arn:aws:iam::123456789012:role/tier2",
			"autonomous":        "arn:aws:iam::123456789012:role/tier3",
		},
	)
	if err != nil {
		t.Fatalf("NewEnvironmentConfig: %v", err)
	}

	want := validConfig()
	if got.Provider != want.Provider {
		t.Errorf("Provider = %q, want %q", got.Provider, want.Provider)
	}
	for key, wantVal := range want.Settings {
		if got.Settings[key] != wantVal {
			t.Errorf("Settings[%q] = %q, want %q", key, got.Settings[key], wantVal)
		}
	}

	// The built config actually works against this broker.
	fake := &fakeSTS{}
	if _, err := New(fake).MintCredentials(context.Background(), got, identity.TierAutonomous); err != nil {
		t.Errorf("MintCredentials with built config: %v", err)
	}
}

func TestNewEnvironmentConfig_UnknownTierName(t *testing.T) {
	_, err := NewEnvironmentConfig("123456789012", "ext-id", "arn:aws:iam::123456789012:role/worker",
		map[string]string{"super_tier": "arn:aws:iam::123456789012:role/tier4"})
	if !errors.Is(err, identity.ErrUnknownTierName) {
		t.Errorf("got %v, want ErrUnknownTierName", err)
	}
}
