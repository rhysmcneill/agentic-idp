package poller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/pkg/cloud"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
	"github.com/rhysmcneill/agentic-idp/worker/internal/controlplane"
)

// fakeCP is a fake controlPlaneClient. jobs/runs are each consumed in order;
// nil means "nothing pending" for that poll. results/runResults record every
// report call.
type fakeCP struct {
	mu         sync.Mutex
	jobs       []*controlplane.Job
	results    []map[string]controlplane.TierResult
	runs       []*controlplane.Run
	runResults []controlplane.RunResult
	runIDs     []string
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

func (f *fakeCP) NextRun(context.Context) (*controlplane.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.runs) == 0 {
		return nil, nil
	}
	run := f.runs[0]
	f.runs = f.runs[1:]
	return run, nil
}

func (f *fakeCP) ReportRunResult(_ context.Context, runID string, result controlplane.RunResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runIDs = append(f.runIDs, runID)
	f.runResults = append(f.runResults, result)
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
	pollVerifications(context.Background(), cp, &fakeBroker{})

	if len(cp.results) != 0 {
		t.Errorf("ReportResult called %d times, want 0", len(cp.results))
	}
}

func TestRunOnce_AllTiersSucceed(t *testing.T) {
	cp := &fakeCP{jobs: []*controlplane.Job{awsJob()}}
	pollVerifications(context.Background(), cp, &fakeBroker{})

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
	pollVerifications(context.Background(), cp, broker)

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
	pollVerifications(context.Background(), cp, &fakeBroker{})

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
		Run(ctx, cp, &fakeBroker{}, &fakeAdapter{}, &fakeGitHubCredential{}, time.Millisecond)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

// fakeAdapter lets tests control Trigger's outcome without calling GitHub.
type fakeAdapter struct {
	failWith error
	calls    []ci.TriggerRequest
}

func (f *fakeAdapter) Trigger(_ context.Context, _ ci.Config, req ci.TriggerRequest) (ci.Handle, error) {
	f.calls = append(f.calls, req)
	if f.failWith != nil {
		return ci.Handle{}, f.failWith
	}
	return ci.Handle{Provider: ci.ProviderGitHubActions, Correlation: req.Correlation}, nil
}

// fakeGitHubCredential lets tests control InstallationToken's outcome.
type fakeGitHubCredential struct {
	failWith error
}

func (f *fakeGitHubCredential) InstallationToken(context.Context, string) (ci.Secret, error) {
	if f.failWith != nil {
		return "", f.failWith
	}
	return "fake-installation-token", nil
}

func githubRun() *controlplane.Run {
	return &controlplane.Run{
		RunID:       "run-1",
		Provider:    string(ci.ProviderGitHubActions),
		WorkflowRef: "deploy.yml",
		Settings:    map[string]string{"repo": "acme/widgets"},
	}
}

func TestPollRuns_NoRun(t *testing.T) {
	cp := &fakeCP{}
	pollRuns(context.Background(), cp, &fakeAdapter{}, &fakeGitHubCredential{})

	if len(cp.runResults) != 0 {
		t.Errorf("ReportRunResult called %d times, want 0", len(cp.runResults))
	}
}

func TestPollRuns_DispatchesSuccessfully(t *testing.T) {
	cp := &fakeCP{runs: []*controlplane.Run{githubRun()}}
	adapter := &fakeAdapter{}
	pollRuns(context.Background(), cp, adapter, &fakeGitHubCredential{})

	if len(cp.runResults) != 0 {
		t.Errorf("ReportRunResult called %d times, want 0 (a successful dispatch reports nothing yet)", len(cp.runResults))
	}
	if len(adapter.calls) != 1 {
		t.Fatalf("Trigger called %d times, want 1", len(adapter.calls))
	}
	if adapter.calls[0].Workflow != "deploy.yml" {
		t.Errorf("Workflow = %q, want %q", adapter.calls[0].Workflow, "deploy.yml")
	}
	if adapter.calls[0].Ref != "main" {
		t.Errorf("Ref = %q, want default %q", adapter.calls[0].Ref, "main")
	}
	if adapter.calls[0].Correlation != "run-1" {
		t.Errorf("Correlation = %q, want %q", adapter.calls[0].Correlation, "run-1")
	}
}

func TestPollRuns_UsesRefSetting(t *testing.T) {
	cp := &fakeCP{runs: []*controlplane.Run{{
		RunID: "run-1", Provider: string(ci.ProviderGitHubActions), WorkflowRef: "deploy.yml",
		Settings: map[string]string{"repo": "acme/widgets", "ref": "release"},
	}}}
	adapter := &fakeAdapter{}
	pollRuns(context.Background(), cp, adapter, &fakeGitHubCredential{})

	if adapter.calls[0].Ref != "release" {
		t.Errorf("Ref = %q, want %q", adapter.calls[0].Ref, "release")
	}
}

func TestPollRuns_UnsupportedProvider_ReportsFailed(t *testing.T) {
	run := githubRun()
	run.Provider = "gitlab_ci"
	cp := &fakeCP{runs: []*controlplane.Run{run}}
	pollRuns(context.Background(), cp, &fakeAdapter{}, &fakeGitHubCredential{})

	if len(cp.runResults) != 1 {
		t.Fatalf("ReportRunResult called %d times, want 1", len(cp.runResults))
	}
	if cp.runResults[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", cp.runResults[0].Status, "failed")
	}
}

func TestPollRuns_NoGitHubCredential_ReportsFailed(t *testing.T) {
	cp := &fakeCP{runs: []*controlplane.Run{githubRun()}}
	pollRuns(context.Background(), cp, &fakeAdapter{}, nil)

	if len(cp.runResults) != 1 {
		t.Fatalf("ReportRunResult called %d times, want 1", len(cp.runResults))
	}
	if cp.runResults[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", cp.runResults[0].Status, "failed")
	}
}

func TestPollRuns_CredentialResolutionFails_ReportsFailed(t *testing.T) {
	cp := &fakeCP{runs: []*controlplane.Run{githubRun()}}
	pollRuns(context.Background(), cp, &fakeAdapter{}, &fakeGitHubCredential{failWith: errors.New("fake: installation not found")})

	if len(cp.runResults) != 1 {
		t.Fatalf("ReportRunResult called %d times, want 1", len(cp.runResults))
	}
	if cp.runResults[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", cp.runResults[0].Status, "failed")
	}
}

func TestPollRuns_TriggerFails_ReportsFailed(t *testing.T) {
	cp := &fakeCP{runs: []*controlplane.Run{githubRun()}}
	adapter := &fakeAdapter{failWith: errors.New("fake: github api error")}
	pollRuns(context.Background(), cp, adapter, &fakeGitHubCredential{})

	if len(cp.runResults) != 1 {
		t.Fatalf("ReportRunResult called %d times, want 1", len(cp.runResults))
	}
	if cp.runIDs[0] != "run-1" {
		t.Errorf("reported result for run %q, want %q", cp.runIDs[0], "run-1")
	}
	if cp.runResults[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", cp.runResults[0].Status, "failed")
	}
}
