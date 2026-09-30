// Package api implements the control plane's HTTP surface.
package api

import (
	"database/sql"
	"net/http"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/credential"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/execution"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/pipeline"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/session"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/verification"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// Server holds the dependencies handlers are built from. It has no handler
// methods of its own — each handler is a standalone function that takes
// exactly what it needs as an argument, not implicit access to everything
// here, so what a handler depends on is visible at its call site in Routes.
type Server struct {
	db *sql.DB

	tenants       *tenant.Store
	teams         *team.Store
	actors        *actor.Store
	environments  *environment.Store
	credentials   *credential.Store
	audits        *audit.Store
	sessions      *session.Store
	workers       *workercred.Store
	verifications *verification.Store
	pipelines     *pipeline.Store

	issuer   *identity.Issuer
	verifier *identity.Verifier

	// workerBootstrapToken gates POST /v1/workers/bootstrap. Empty disables
	// the route entirely — see EnableWorkerBootstrap.
	workerBootstrapToken []byte

	// runs is nil until EnableExecution is called, which gates the
	// runs/decision routes the same way workerBootstrapToken gates worker
	// bootstrap — its River client needs a pgxpool.Pool this package doesn't
	// otherwise construct, so server.Run builds it and wires it in.
	runs *execution.Store
}

// NewServer constructs a Server. db is used both directly (for stores that
// don't need a transaction) and to open transactions for handlers that
// compose several stores atomically (setup).
func NewServer(db *sql.DB, issuer *identity.Issuer, verifier *identity.Verifier) *Server {
	return &Server{
		db:            db,
		tenants:       tenant.NewStore(db),
		teams:         team.NewStore(db),
		actors:        actor.NewStore(db),
		environments:  environment.NewStore(db),
		credentials:   credential.NewStore(db),
		audits:        audit.NewStore(db),
		sessions:      session.NewStore(db),
		workers:       workercred.NewStore(db),
		verifications: verification.NewStore(db),
		pipelines:     pipeline.NewStore(db),
		issuer:        issuer,
		verifier:      verifier,
	}
}

// EnableExecution turns on the runs/decision routes, backed by runs (built
// by server.Run over its own River-connected pgxpool.Pool — see
// controlplane/internal/execution.NewQueueClient). Leave it uncalled to keep
// the routes disabled, its default state, e.g. in tests that don't need them.
func (s *Server) EnableExecution(runs *execution.Store) {
	s.runs = runs
}

// EnableWorkerBootstrap turns on POST /v1/workers/bootstrap, authenticated by
// token instead of a session. Call it only with a non-empty token — leave it
// uncalled to keep the route disabled, its default state.
func (s *Server) EnableWorkerBootstrap(token string) {
	s.workerBootstrapToken = []byte(token)
}

// Routes builds the HTTP handler for the whole API surface. Every route is
// listed here, each wired to exactly the dependencies its handler needs.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/setup", handleGetSetup(s.tenants))
	mux.Handle("POST /v1/setup", handlePostSetup(s.db, s.tenants, s.issuer))
	mux.Handle("POST /v1/token", handlePostToken(s.actors, s.credentials, s.audits, s.issuer))
	mux.Handle("POST /v1/agents", requireAuth(s.verifier, s.sessions,
		handlePostAgents(s.teams, s.environments, s.actors, s.audits, s.issuer)))
	mux.Handle("POST /v1/environments", requireAuth(s.verifier, s.sessions,
		handlePostEnvironments(s.environments, s.audits)))
	mux.Handle("POST /v1/workers", requireAuth(s.verifier, s.sessions,
		handlePostWorkers(s.environments, s.workers, s.audits)))
	mux.Handle("GET /v1/workers", requireAuth(s.verifier, s.sessions,
		handleGetWorkers(s.environments, s.workers)))
	mux.Handle("POST /v1/workers/{id}/environments", requireAuth(s.verifier, s.sessions,
		handlePostWorkerGrantEnvironments(s.environments, s.workers, s.audits)))
	if len(s.workerBootstrapToken) > 0 {
		mux.Handle("POST /v1/workers/bootstrap",
			handlePostWorkerBootstrap(s.workerBootstrapToken, s.tenants, s.actors, s.workers, s.audits))
	}
	mux.Handle("POST /v1/environments/{name}/verify", requireAuth(s.verifier, s.sessions,
		handlePostEnvironmentVerify(s.environments, s.verifications, s.audits)))
	mux.Handle("GET /v1/environments/{name}/verifications/{id}", requireAuth(s.verifier, s.sessions,
		handleGetVerification(s.environments, s.verifications)))
	mux.Handle("GET /v1/worker/verifications/next", requireWorkerAuth(s.workers,
		handleGetNextWorkerVerification(s.environments, s.verifications)))
	mux.Handle("POST /v1/worker/verifications/{id}/result", requireWorkerAuth(s.workers,
		handlePostWorkerVerificationResult(s.verifications)))
	mux.Handle("PUT /v1/worker/ci-callback-url", requireWorkerAuth(s.workers,
		handlePutWorkerCICallbackURL(s.workers)))
	mux.Handle("GET /v1/ci/callback-url", handleGetCICallbackURL(s.workers))
	mux.Handle("POST /v1/pipelines", requireAuth(s.verifier, s.sessions,
		handlePostPipelines(s.environments, s.pipelines)))
	mux.Handle("GET /v1/pipelines/{id}", requireAuth(s.verifier, s.sessions,
		handleGetPipeline(s.pipelines)))
	if s.runs != nil {
		mux.Handle("POST /v1/runs", requireAuth(s.verifier, s.sessions,
			handlePostRuns(s.environments, s.runs, s.audits)))
		mux.Handle("GET /v1/runs/{id}", requireAuth(s.verifier, s.sessions,
			handleGetRun(s.runs)))
		mux.Handle("POST /v1/runs/{id}/decision", requireAuth(s.verifier, s.sessions,
			handlePostRunDecision(s.runs, s.audits)))
		mux.Handle("GET /v1/worker/runs/next", requireWorkerAuth(s.workers,
			handleGetNextWorkerRun(s.runs, s.pipelines)))
		mux.Handle("POST /v1/worker/runs/{id}/result", requireWorkerAuth(s.workers,
			handlePostWorkerRunResult(s.runs)))
		mux.Handle("POST /v1/worker/runs/resolve", requireWorkerAuth(s.workers,
			handlePostWorkerRunResolve(s.runs, s.pipelines, s.environments, s.audits)))
	}
	return mux
}
