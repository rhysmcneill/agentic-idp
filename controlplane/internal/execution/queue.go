package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/policy"
	"github.com/rhysmcneill/agentic-idp/pkg/ci"
)

// runJobTimeout is River's lease timeout for a claimed job (Decision 023), not a library default.
const runJobTimeout = 5 * time.Minute

// runJobMaxAttempts bounds River's retry-with-backoff before discarding a job.
const runJobMaxAttempts = 5

// JobArgs is River's job payload: just enough to reload the Run; never the tier or credential itself.
type JobArgs struct {
	RunID uuid.UUID `json:"run_id"`
}

// Kind implements river.JobArgs.
func (JobArgs) Kind() string { return "run" }

// Worker moves a queued Run to StatusExecuting;
type Worker struct {
	river.WorkerDefaults[JobArgs]
	store    *Store
	adapters *ci.Registry
}

// Timeout overrides the client-level default with runJobTimeout.
func (w *Worker) Timeout(*river.Job[JobArgs]) time.Duration { return runJobTimeout }

// Work claims the Run, re-checks policy as the credential-mint gate (issue #3), then resolves a pkg/ci.Adapter;
func (w *Worker) Work(ctx context.Context, job *river.Job[JobArgs]) error {
	claimed, err := w.store.transitionStatus(ctx, job.Args.RunID, StatusQueued, StatusExecuting)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // redelivered job, already handled
	}
	if err != nil {
		return fmt.Errorf("execution: work: claiming run: %w", err)
	}

	p, err := w.store.pipelines.Get(ctx, claimed.TenantID, claimed.PipelineID)
	if err != nil {
		return fmt.Errorf("execution: work: loading pipeline: %w", err)
	}

	if policy.Check(claimed.Tier, p.Mutating) != policy.Allow {
		return w.failClosed(ctx, claimed.ID)
	}

	if _, err := w.adapters.Get(p.Provider); errors.Is(err, ci.ErrUnknownProvider) {
		return w.failClosed(ctx, claimed.ID)
	} else if err != nil {
		return fmt.Errorf("execution: work: resolving adapter: %w", err)
	}
	return nil
}

// failClosed guardedly moves runID from StatusExecuting to StatusFailed.
func (w *Worker) failClosed(ctx context.Context, runID uuid.UUID) error {
	_, err := w.store.transitionStatus(ctx, runID, StatusExecuting, StatusFailed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("execution: work: failing run closed: %w", err)
	}
	return nil
}

// Enqueuer is implemented by Queue.
var _ Enqueuer = (*Queue)(nil)

// Queue wraps a River client scoped to run's job kind.
type Queue struct {
	client *river.Client[pgx.Tx]
}

// Enqueue inserts a River job that moves runID to StatusExecuting once claimed.
func (q *Queue) Enqueue(ctx context.Context, runID uuid.UUID) error {
	_, err := q.client.Insert(ctx, JobArgs{RunID: runID}, &river.InsertOpts{MaxAttempts: runJobMaxAttempts})
	if err != nil {
		return fmt.Errorf("execution: enqueue: %w", err)
	}
	return nil
}

// deferredEnqueuer breaks the Store<->Queue constructor cycle by letting Store be built before Queue exists.
type deferredEnqueuer struct {
	target Enqueuer
}

func (d *deferredEnqueuer) Enqueue(ctx context.Context, runID uuid.UUID) error {
	if d.target == nil {
		return fmt.Errorf("execution: enqueuer not yet configured")
	}
	if err := d.target.Enqueue(ctx, runID); err != nil {
		return fmt.Errorf("execution: enqueue: %w", err)
	}
	return nil
}

// NewQueueClient builds the Store (over db) and its River client (over pool) together; db and pool must share one database.
func NewQueueClient(db sqlcgen.DBTX, pipelines *pipeline.Store, adapters *ci.Registry, pool *pgxpool.Pool, maxWorkers int) (*Store, *river.Client[pgx.Tx], error) {
	enq := &deferredEnqueuer{}
	store := NewStore(db, pipelines, enq)

	workers := river.NewWorkers()
	river.AddWorker(workers, &Worker{store: store, adapters: adapters})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("execution: new queue client: %w", err)
	}

	queue := &Queue{client: client}
	enq.target = queue
	return store, client, nil
}
