// Package telemetry sends an opt-in, aggregate-only usage snapshot to an
// operator-configured endpoint.
package telemetry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/telemetry/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
)

// Interval is how often a snapshot is sent, when enabled.
const Interval = 24 * time.Hour

// Payload is the aggregate, non-identifying snapshot sent to the configured
// endpoint — no run details, account IDs, repo names, cost figures, or names.
type Payload struct {
	InstanceID       string    `json:"instance_id"`
	ProductVersion   string    `json:"product_version"`
	ActiveAgentCount int64     `json:"active_agent_count"`
	RunCount24h      int64     `json:"run_count_24h"`
	PeriodStart      time.Time `json:"period_start"`
	PeriodEnd        time.Time `json:"period_end"`
}

// Sender periodically builds and sends a Payload while the instance's
// tenant has opted in.
type Sender struct {
	endpoint       string
	productVersion string
	http           *http.Client
	tenants        *tenant.Store
	q              *sqlcgen.Queries
}

// NewSender constructs a Sender; an empty endpoint means Run never sends anything.
func NewSender(endpoint, productVersion string, db sqlcgen.DBTX, tenants *tenant.Store) *Sender {
	return &Sender{
		endpoint:       endpoint,
		productVersion: productVersion,
		http:           &http.Client{Timeout: 10 * time.Second},
		tenants:        tenants,
		q:              sqlcgen.New(db),
	}
}

// Run ticks every Interval, sending one Payload per tick only when the
// tenant has opted in. No retries — a failed send just waits for the next tick.
func (s *Sender) Run(ctx context.Context) {
	if s.endpoint == "" {
		return
	}

	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Sender) tick(ctx context.Context) {
	t, err := s.tenants.GetSole(ctx)
	if errors.Is(err, tenant.ErrNotFound) {
		return // not set up yet
	}
	if err != nil {
		slog.Error("telemetry: loading tenant", "error", err)
		return
	}
	if !t.TelemetryEnabled {
		return
	}

	payload, err := s.buildPayload(ctx)
	if err != nil {
		slog.Error("telemetry: building payload", "error", err)
		return
	}

	if err := s.send(ctx, payload); err != nil {
		slog.Error("telemetry: sending", "error", err)
	}
}

func (s *Sender) buildPayload(ctx context.Context) (Payload, error) {
	instanceID, err := LoadOrCreateInstanceID(ctx, s.q)
	if err != nil {
		return Payload{}, fmt.Errorf("loading instance id: %w", err)
	}

	agentCount, err := s.q.CountActiveAgents(ctx)
	if err != nil {
		return Payload{}, fmt.Errorf("counting active agents: %w", err)
	}

	periodEnd := time.Now().UTC()
	periodStart := periodEnd.Add(-Interval)
	runCount, err := s.q.CountRunsSince(ctx, periodStart)
	if err != nil {
		return Payload{}, fmt.Errorf("counting recent runs: %w", err)
	}

	return Payload{
		InstanceID:       instanceID,
		ProductVersion:   s.productVersion,
		ActiveAgentCount: agentCount,
		RunCount24h:      runCount,
		PeriodStart:      periodStart,
		PeriodEnd:        periodEnd,
	}, nil
}

func (s *Sender) send(ctx context.Context, payload Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("posting: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

// LoadOrCreateInstanceID returns this deployment's random telemetry
// identifier (deliberately not the tenant ID), generating one if needed —
// same pattern as controlplane/internal/signingkey.LoadOrCreate.
func LoadOrCreateInstanceID(ctx context.Context, q *sqlcgen.Queries) (string, error) {
	row, err := q.GetTelemetryInstance(ctx)
	if err == nil {
		return row.ID.String(), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("loading: %w", err)
	}

	created, err := q.CreateTelemetryInstance(ctx)
	if err == nil {
		return created.ID.String(), nil
	}

	// Another instance won the race to create the singleton row first —
	// read back what it wrote rather than treating this as a failure.
	row, getErr := q.GetTelemetryInstance(ctx)
	if getErr != nil {
		return "", fmt.Errorf("creating: %w (and re-reading after conflict: %v)", err, getErr)
	}
	return row.ID.String(), nil
}
