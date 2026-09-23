// Package environment stores Environment rows: tier role ARNs, external IDs,
// trust anchors. See docs/DATA-MODEL.md "environments".
package environment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// ErrNotFound is returned by Get, GetAWSConfig and GetAWSTierRole when no row
// matches.
var ErrNotFound = errors.New("environment: not found")

// Environment is provider-agnostic; per-provider identity and credential
// binding live in AWSConfig/AWSTierRole rather than nullable columns here.
type Environment struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	Provider  cloud.Provider
	Region    string
	CreatedAt time.Time
}

// AWSConfig is the account-level AWS identity for an Environment: which
// account, the external ID for the confused-deputy check, and the principal
// trusted to assume into it.
type AWSConfig struct {
	EnvironmentID uuid.UUID
	AccountRef    string
	ExternalID    string
	TrustAnchor   string
}

// AWSTierRole is the per-tier IAM role binding for an Environment: which role
// a ReadOnly/HumanInTheLoop/Autonomous action assumes.
type AWSTierRole struct {
	EnvironmentID uuid.UUID
	Tier          identity.Tier
	RoleARN       string
}

// Store is a Postgres-backed environment repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new environment and returns the stored row.
func (s *Store) Create(ctx context.Context, tenantID uuid.UUID, name string, provider cloud.Provider, region string) (Environment, error) {
	if tenantID == uuid.Nil {
		return Environment{}, fmt.Errorf("environment: create: tenant id is required")
	}
	if name == "" {
		return Environment{}, fmt.Errorf("environment: create: name is required")
	}
	if region == "" {
		return Environment{}, fmt.Errorf("environment: create: region is required")
	}

	row, err := s.q.CreateEnvironment(ctx, sqlcgen.CreateEnvironmentParams{
		TenantID: tenantID,
		Name:     name,
		Provider: string(provider),
		Region:   region,
	})
	if err != nil {
		return Environment{}, fmt.Errorf("environment: create: %w", err)
	}
	return fromRow(row), nil
}

// Get returns the environment with the given ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Environment, error) {
	row, err := s.q.GetEnvironment(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, fmt.Errorf("environment: get: %w", err)
	}
	return fromRow(row), nil
}

// GetByName returns the environment named name within tenantID, or
// ErrNotFound. Used to resolve an operator-supplied environment name (e.g.
// "staging") at agent enrolment time.
func (s *Store) GetByName(ctx context.Context, tenantID uuid.UUID, name string) (Environment, error) {
	row, err := s.q.GetEnvironmentByName(ctx, sqlcgen.GetEnvironmentByNameParams{
		TenantID: tenantID,
		Name:     name,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, fmt.Errorf("environment: get by name: %w", err)
	}
	return fromRow(row), nil
}

// CreateAWSConfig registers the account-level AWS identity for environmentID.
// One row per environment; a second call fails on the primary key.
func (s *Store) CreateAWSConfig(ctx context.Context, environmentID uuid.UUID, accountRef, externalID, trustAnchor string) (AWSConfig, error) {
	if accountRef == "" || externalID == "" || trustAnchor == "" {
		return AWSConfig{}, fmt.Errorf("environment: create aws config: account ref, external id and trust anchor are all required")
	}

	row, err := s.q.CreateEnvironmentAWSConfig(ctx, sqlcgen.CreateEnvironmentAWSConfigParams{
		EnvironmentID: environmentID,
		AccountRef:    accountRef,
		ExternalID:    externalID,
		TrustAnchor:   trustAnchor,
	})
	if err != nil {
		return AWSConfig{}, fmt.Errorf("environment: create aws config: %w", err)
	}
	return AWSConfig{
		EnvironmentID: row.EnvironmentID,
		AccountRef:    row.AccountRef,
		ExternalID:    row.ExternalID,
		TrustAnchor:   row.TrustAnchor,
	}, nil
}

// GetAWSConfig returns the AWS account-level config for environmentID, or
// ErrNotFound.
func (s *Store) GetAWSConfig(ctx context.Context, environmentID uuid.UUID) (AWSConfig, error) {
	row, err := s.q.GetEnvironmentAWSConfig(ctx, environmentID)
	if errors.Is(err, sql.ErrNoRows) {
		return AWSConfig{}, ErrNotFound
	}
	if err != nil {
		return AWSConfig{}, fmt.Errorf("environment: get aws config: %w", err)
	}
	return AWSConfig{
		EnvironmentID: row.EnvironmentID,
		AccountRef:    row.AccountRef,
		ExternalID:    row.ExternalID,
		TrustAnchor:   row.TrustAnchor,
	}, nil
}

// CreateAWSTierRole registers the IAM role a given tier assumes in
// environmentID. Called once per tier at registration.
func (s *Store) CreateAWSTierRole(ctx context.Context, environmentID uuid.UUID, tier identity.Tier, roleARN string) (AWSTierRole, error) {
	if !tier.Valid() {
		return AWSTierRole{}, fmt.Errorf("environment: create aws tier role: invalid tier %d", tier)
	}
	if roleARN == "" {
		return AWSTierRole{}, fmt.Errorf("environment: create aws tier role: role arn is required")
	}

	row, err := s.q.CreateEnvironmentAWSTierRole(ctx, sqlcgen.CreateEnvironmentAWSTierRoleParams{
		EnvironmentID: environmentID,
		Tier:          int16(tier), // #nosec G115 -- Valid() above guarantees 1-3
		RoleArn:       roleARN,
	})
	if err != nil {
		return AWSTierRole{}, fmt.Errorf("environment: create aws tier role: %w", err)
	}
	return AWSTierRole{EnvironmentID: row.EnvironmentID, Tier: identity.Tier(row.Tier), RoleARN: row.RoleArn}, nil
}

// GetAWSTierRole returns the IAM role binding for environmentID at tier, or
// ErrNotFound. MintCredentials looks this up before calling sts:AssumeRole.
func (s *Store) GetAWSTierRole(ctx context.Context, environmentID uuid.UUID, tier identity.Tier) (AWSTierRole, error) {
	if !tier.Valid() {
		return AWSTierRole{}, fmt.Errorf("environment: get aws tier role: invalid tier %d", tier)
	}

	row, err := s.q.GetEnvironmentAWSTierRole(ctx, sqlcgen.GetEnvironmentAWSTierRoleParams{
		EnvironmentID: environmentID,
		Tier:          int16(tier), // #nosec G115 -- Valid() above guarantees 1-3
	})
	if errors.Is(err, sql.ErrNoRows) {
		return AWSTierRole{}, ErrNotFound
	}
	if err != nil {
		return AWSTierRole{}, fmt.Errorf("environment: get aws tier role: %w", err)
	}
	return AWSTierRole{EnvironmentID: row.EnvironmentID, Tier: identity.Tier(row.Tier), RoleARN: row.RoleArn}, nil
}

func fromRow(row sqlcgen.Environment) Environment {
	return Environment{
		ID:        row.ID,
		TenantID:  row.TenantID,
		Name:      row.Name,
		Provider:  cloud.Provider(row.Provider),
		Region:    row.Region,
		CreatedAt: row.CreatedAt,
	}
}
