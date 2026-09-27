// Package pipeline stores Pipeline rows: which CI provider and workflow a
// run targets, and whether it can mutate its Environment. See
// docs/DATA-MODEL.md "pipelines".
package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
)

// ErrNotFound is returned by Get when no row matches.
var ErrNotFound = errors.New("pipeline: not found")

// Pipeline is the per-Environment configuration for one CI provider. Mutating
// drives the ReadOnly tier's policy check (docs/SECURITY-MODEL.md): ReadOnly
// may trigger a non-mutating pipeline (e.g. terraform plan) unattended,
// never a mutating one.
type Pipeline struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	EnvironmentID uuid.UUID
	Provider      ci.Provider
	WorkflowRef   string
	Settings      map[string]string
	Mutating      bool
	CreatedAt     time.Time
}

// Store is a Postgres-backed pipeline repository.
type Store struct {
	q *sqlcgen.Queries
}

// NewStore constructs a Store over db, which may be a *sql.DB for normal use
// or a *sql.Tx to compose with other stores inside one transaction.
func NewStore(db sqlcgen.DBTX) *Store {
	return &Store{q: sqlcgen.New(db)}
}

// Create inserts a new pipeline and returns the stored row.
func (s *Store) Create(ctx context.Context, tenantID, environmentID uuid.UUID, provider ci.Provider, workflowRef string, settings map[string]string, mutating bool) (Pipeline, error) {
	if tenantID == uuid.Nil {
		return Pipeline{}, fmt.Errorf("pipeline: create: tenant id is required")
	}
	if environmentID == uuid.Nil {
		return Pipeline{}, fmt.Errorf("pipeline: create: environment id is required")
	}
	if workflowRef == "" {
		return Pipeline{}, fmt.Errorf("pipeline: create: workflow ref is required")
	}

	settingsJSON, err := marshalSettings(settings)
	if err != nil {
		return Pipeline{}, fmt.Errorf("pipeline: create: %w", err)
	}

	row, err := s.q.CreatePipeline(ctx, sqlcgen.CreatePipelineParams{
		TenantID:      tenantID,
		EnvironmentID: environmentID,
		Provider:      string(provider),
		WorkflowRef:   workflowRef,
		Settings:      settingsJSON,
		Mutating:      mutating,
	})
	if err != nil {
		return Pipeline{}, fmt.Errorf("pipeline: create: %w", err)
	}
	return fromRow(row)
}

// Get returns the pipeline with the given ID within tenantID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, tenantID, id uuid.UUID) (Pipeline, error) {
	row, err := s.q.GetPipeline(ctx, sqlcgen.GetPipelineParams{
		ID:       id,
		TenantID: tenantID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Pipeline{}, ErrNotFound
	}
	if err != nil {
		return Pipeline{}, fmt.Errorf("pipeline: get: %w", err)
	}
	return fromRow(row)
}

func marshalSettings(settings map[string]string) (json.RawMessage, error) {
	if settings == nil {
		return json.RawMessage(`{}`), nil
	}
	b, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshalling settings: %w", err)
	}
	return b, nil
}

func fromRow(row sqlcgen.Pipeline) (Pipeline, error) {
	settings := map[string]string{}
	if len(row.Settings) > 0 {
		if err := json.Unmarshal(row.Settings, &settings); err != nil {
			return Pipeline{}, fmt.Errorf("unmarshalling settings: %w", err)
		}
	}
	return Pipeline{
		ID:            row.ID,
		TenantID:      row.TenantID,
		EnvironmentID: row.EnvironmentID,
		Provider:      ci.Provider(row.Provider),
		WorkflowRef:   row.WorkflowRef,
		Settings:      settings,
		Mutating:      row.Mutating,
		CreatedAt:     row.CreatedAt,
	}, nil
}
