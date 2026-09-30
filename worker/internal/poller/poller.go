// Package poller runs the worker's main loop: poll the control plane for a
// pending connectivity-check or run job, execute it, and report the result back.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
	"github.com/rhysmcneill/agentic-idp/worker/internal/broker/aws"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
)

// controlPlaneClient is the subset of controlplane.Client this package
// calls — a small interface at the point of use, so tests substitute a fake.
type controlPlaneClient interface {
	NextJob(ctx context.Context) (*controlplane.Job, error)
	ReportResult(ctx context.Context, verificationID string, results map[string]controlplane.TierResult) error
	NextRun(ctx context.Context) (*controlplane.Run, error)
	ReportRunResult(ctx context.Context, runID string, result controlplane.RunResult) error
}

// ciAdapter is the subset of pkg/ci.Adapter this package calls. Resolving
// and status-polling happen in the CI OIDC callback, not here.
type ciAdapter interface {
	Trigger(ctx context.Context, cfg ci.Config, req ci.TriggerRequest) (ci.Handle, error)
}

// githubCredential resolves a short-lived GitHub API token for one repo.
// nil is valid on a worker that never claims a github_actions run.
type githubCredential interface {
	InstallationToken(ctx context.Context, repo string) (ci.Secret, error)
}

// Run polls cp for verification and run jobs every interval until ctx is cancelled.
func Run(ctx context.Context, cp controlPlaneClient, broker cloud.Broker, adapter ciAdapter, githubCreds githubCredential, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		pollVerifications(ctx, cp, broker)
		pollRuns(ctx, cp, adapter, githubCreds)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func pollVerifications(ctx context.Context, cp controlPlaneClient, broker cloud.Broker) {
	job, err := cp.NextJob(ctx)
	if err != nil {
		slog.Error("polling for next job", "error", err)
		return
	}
	if job == nil {
		return
	}
	slog.Info("claimed verification job", "verification_id", job.VerificationID, "environment", job.Environment)

	results, err := runChecks(ctx, broker, job)
	if err != nil {
		slog.Error("running verification checks", "verification_id", job.VerificationID, "error", err)
		return
	}

	if err := cp.ReportResult(ctx, job.VerificationID, results); err != nil {
		slog.Error("reporting verification result", "verification_id", job.VerificationID, "error", err)
	}
}

// runChecks calls MintCredentials once per tier in job.RoleARNs, recording
// whether each one succeeded. Only AWS jobs are supported today — a job for
// any other provider fails loudly rather than being silently misinterpreted
// through AWS-shaped config keys.
func runChecks(ctx context.Context, broker cloud.Broker, job *controlplane.Job) (map[string]controlplane.TierResult, error) {
	if job.Provider != string(cloud.ProviderAWS) {
		return nil, fmt.Errorf("poller: no support for provider %q", job.Provider)
	}

	cfg, err := aws.NewEnvironmentConfig(job.AccountRef, job.ExternalID, job.TrustAnchor, job.RoleARNs)
	if err != nil {
		return nil, fmt.Errorf("poller: building environment config: %w", err)
	}

	results := make(map[string]controlplane.TierResult, len(job.RoleARNs))
	for tierName := range job.RoleARNs {
		tier, err := identity.ParseTier(tierName)
		if err != nil {
			results[tierName] = controlplane.TierResult{OK: false, Error: err.Error()}
			continue
		}
		if _, err := broker.MintCredentials(ctx, cfg, tier); err != nil {
			results[tierName] = controlplane.TierResult{OK: false, Error: err.Error()}
			continue
		}
		results[tierName] = controlplane.TierResult{OK: true}
	}
	return results, nil
}

func pollRuns(ctx context.Context, cp controlPlaneClient, adapter ciAdapter, githubCreds githubCredential) {
	run, err := cp.NextRun(ctx)
	if err != nil {
		slog.Error("polling for next run", "error", err)
		return
	}
	if run == nil {
		return
	}
	slog.Info("claimed run", "run_id", run.RunID, "provider", run.Provider)

	if run.Provider != string(ci.ProviderGitHubActions) {
		failRunDispatch(ctx, cp, run.RunID, fmt.Sprintf("no support for provider %q", run.Provider))
		return
	}
	if githubCreds == nil {
		failRunDispatch(ctx, cp, run.RunID, "this worker has no GitHub App credentials configured")
		return
	}

	repo := run.Settings["repo"]
	token, err := githubCreds.InstallationToken(ctx, repo)
	if err != nil {
		failRunDispatch(ctx, cp, run.RunID, fmt.Sprintf("resolving github credential: %v", err))
		return
	}

	ref := run.Settings["ref"]
	if ref == "" {
		ref = "main"
	}

	cfg := ci.Config{Provider: ci.ProviderGitHubActions, Settings: run.Settings, Credential: token}
	if _, err := adapter.Trigger(ctx, cfg, ci.TriggerRequest{Ref: ref, Workflow: run.WorkflowRef, Correlation: run.RunID}); err != nil {
		failRunDispatch(ctx, cp, run.RunID, fmt.Sprintf("triggering pipeline: %v", err))
		return
	}
	slog.Info("dispatched run", "run_id", run.RunID, "workflow", run.WorkflowRef)
}

// failRunDispatch reports a run failed before reaching GitHub — no external ref or URL to record.
func failRunDispatch(ctx context.Context, cp controlPlaneClient, runID, reason string) {
	slog.Error("run failed before dispatch", "run_id", runID, "reason", reason)
	if err := cp.ReportRunResult(ctx, runID, controlplane.RunResult{Status: "failed", CIRawStatus: reason}); err != nil {
		slog.Error("reporting run dispatch failure", "run_id", runID, "error", err)
	}
}
