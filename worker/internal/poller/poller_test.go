package poller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
)

// fakeCP is a fake controlPlaneClient. jobs is consumed in order; nil means
// "nothing pending" for that poll. results records every ReportResult call.
type fakeCP struct {
	mu      sync.Mutex
	jobs    []*controlplane.Job
	results []map[string]controlplane.TierResult
}

func (f *fakeCP) NextJob(context.Context) (*controlplane.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.jobs) == 0 {
		return nil, nil
	}
	job := f.jobs[0]
	f.jobs = f.jobs[1:]
	return job, nil
}

func (f *fakeCP) ReportResult(_ context.Context, _ string, results map[string]controlplane.TierResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, results)
	return nil
}

// fakeBroker lets tests control MintCredentials' outcome per tier without a
// real AWS call.
type fakeBroker struct {
	failTiers map[identity.Tier]bool
}

func (f *fakeBroker) Provider() cloud.Provider { return cloud.ProviderAWS }

func (f *fakeBroker) ValidateEnvironment(context.Context, cloud.EnvironmentConfig) error {
	return nil
}

func (f *fakeBroker) MintCredentials(_ context.Context, _ cloud.EnvironmentConfig, tier identity.Tier) (cloud.Credentials, error) {
	if f.failTiers[tier] {
		return cloud.Credentials{}, errors.New("fake: AccessDenied")
	}
	return cloud.Credentials{Provider: cloud.ProviderAWS}, nil
}

func awsJob() *controlplane.Job {
	return &controlplane.Job{
		VerificationID: "verification-1",
		Environment:    "staging",
		Provider:       "aws",
		AccountRef:     "123456789012",
		ExternalID:     "generated-external-id",
		TrustAnchor:    "arn:aws:iam::123456789012:role/worker",
		RoleARNs: map[string]string{
			"read_only":         "arn:aws:iam::123456789012:role/tier1",
			"human_in_the_loop": "arn:aws:iam::123456789012:role/tier2",
			"autonomous":        "arn:aws:iam::123456789012:role/tier3",
		},
	}
}

func TestRunOnce_NoJob(t *testing.T) {
	cp := &fakeCP{}
	runOnce(context.Background(), cp, &fakeBroker{})

	if len(cp.results) != 0 {
		t.Errorf("ReportResult called %d times, want 0", len(cp.results))
	}
}

func TestRunOnce_AllTiersSucceed(t *testing.T) {
	cp := &fakeCP{jobs: []*controlplane.Job{awsJob()}}
	runOnce(context.Background(), cp, &fakeBroker{})

	if len(cp.results) != 1 {
		t.Fatalf("ReportResult called %d times, want 1", len(cp.results))
	}
	for tier, r := range cp.results[0] {
		if !r.OK {
			t.Errorf("tier %q: OK = false, want true", tier)
		}
	}
	if len(cp.results[0]) != 3 {
		t.Errorf("got %d tier results, want 3", len(cp.results[0]))
	}
}

func TestRunOnce_OneTierFails(t *testing.T) {
	cp := &fakeCP{jobs: []*controlplane.Job{awsJob()}}
	broker := &fakeBroker{failTiers: map[identity.Tier]bool{identity.TierAutonomous: true}}
	runOnce(context.Background(), cp, broker)

	if len(cp.results) != 1 {
		t.Fatalf("ReportResult called %d times, want 1", len(cp.results))
	}
	if cp.results[0]["autonomous"].OK {
		t.Error("autonomous tier reported OK, want failed")
	}
	if cp.results[0]["autonomous"].Error == "" {
		t.Error("autonomous tier result has no error message")
	}
	if !cp.results[0]["read_only"].OK {
		t.Error("read_only tier reported failed, want OK")
	}
}

func TestRunOnce_UnsupportedProvider(t *testing.T) {
	job := awsJob()
	job.Provider = "gcp"
	cp := &fakeCP{jobs: []*controlplane.Job{job}}
	runOnce(context.Background(), cp, &fakeBroker{})

	// No result is ever reported for an unsupported provider — failing
	// loudly in the log, not silently reporting a fabricated success.
	if len(cp.results) != 0 {
		t.Errorf("ReportResult called %d times, want 0", len(cp.results))
	}
}

func TestRun_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cp := &fakeCP{}

	done := make(chan struct{})
	go func() {
		Run(ctx, cp, &fakeBroker{}, time.Millisecond)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}
