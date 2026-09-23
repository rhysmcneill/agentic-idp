// Package poller runs the worker's main loop: poll the control plane for a
// pending connectivity-check job, execute it against the cloud broker, and
// report the result back.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
}

// Run polls cp for verification jobs every interval, executing each one
// against broker, until ctx is cancelled.
func Run(ctx context.Context, cp controlPlaneClient, broker cloud.Broker, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		runOnce(ctx, cp, broker)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runOnce(ctx context.Context, cp controlPlaneClient, broker cloud.Broker) {
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
