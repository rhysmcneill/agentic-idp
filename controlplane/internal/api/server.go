// Package api implements the control plane's HTTP surface.
package api

import (
	"database/sql"
	"net/http"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/credential"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
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

	issuer   *identity.Issuer
	verifier *identity.Verifier
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
		issuer:        issuer,
		verifier:      verifier,
	}
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
	mux.Handle("POST /v1/environments/{name}/verify", requireAuth(s.verifier, s.sessions,
		handlePostEnvironmentVerify(s.environments, s.verifications, s.audits)))
	mux.Handle("GET /v1/environments/{name}/verifications/{id}", requireAuth(s.verifier, s.sessions,
		handleGetVerification(s.environments, s.verifications)))
	mux.Handle("GET /v1/worker/verifications/next", requireWorkerAuth(s.workers,
		handleGetNextWorkerVerification(s.environments, s.verifications)))
	mux.Handle("POST /v1/worker/verifications/{id}/result", requireWorkerAuth(s.workers,
		handlePostWorkerVerificationResult(s.verifications)))
	return mux
}
