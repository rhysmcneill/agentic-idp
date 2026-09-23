package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/credential"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/tenant"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// minPasswordLength is a floor, not a complexity rule — see NIST 800-63B on
// why composition rules (must contain a digit, etc.) don't meaningfully
// improve password strength.
const minPasswordLength = 12

// defaultTeamName is used for the team POST /v1/setup creates. The operator
// isn't asked for one — a second team is catalog/Phase-2 territory, not
// something a fresh instance needs.
const defaultTeamName = "default"

type setupStatusResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

func handleGetSetup(tenants *tenant.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exists, err := tenants.AnyExists(r.Context())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, setupStatusResponse{NeedsSetup: !exists})
	})
}

type setupRequest struct {
	TenantName    string `json:"tenant_name"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
}

type setupResponse struct {
	TenantID string `json:"tenant_id"`
	ActorID  string `json:"actor_id"`
	Token    string `json:"token"`
}

func handlePostSetup(db *sql.DB, tenants *tenant.Store, issuer *identity.Issuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req setupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.TenantName == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "tenant_name is required")
			return
		}
		if req.AdminUsername == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "admin_username is required")
			return
		}
		if len(req.AdminPassword) < minPasswordLength {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("admin_password must be at least %d characters", minPasswordLength))
			return
		}

		ctx := r.Context()

		// Fast-path check before opening a transaction — the common case,
		// once setup has run, is every later request hitting this
		// immediately.
		exists, err := tenants.AnyExists(ctx)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if exists {
			writeError(w, http.StatusGone, codeGone, "setup has already run on this instance")
			return
		}

		// Serializable so two concurrent setup requests against a genuinely
		// fresh instance can't both pass the check above and both succeed —
		// one wins, the other fails with a serialization error, mapped to a
		// conflict below.
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			writeInternalError(w, err)
			return
		}
		defer func() { _ = tx.Rollback() }()

		txTenants := tenant.NewStore(tx)
		txTeams := team.NewStore(tx)
		txActors := actor.NewStore(tx)
		txCredentials := credential.NewStore(tx)
		txAudits := audit.NewStore(tx)

		if exists, err := txTenants.AnyExists(ctx); err != nil {
			writeInternalError(w, err)
			return
		} else if exists {
			writeError(w, http.StatusGone, codeGone, "setup has already run on this instance")
			return
		}

		newTenant, err := txTenants.Create(ctx, req.TenantName)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		newTeam, err := txTeams.Create(ctx, newTenant.ID, defaultTeamName)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		newActor, err := txActors.Create(ctx, actor.CreateParams{
			TenantID:  newTenant.ID,
			Type:      identity.ActorHuman,
			Name:      req.AdminUsername,
			TeamID:    newTeam.ID,
			TrustTier: identity.TierAutonomous,
			// AuthorizedBy left nil: this is the one actor permitted a NULL
			// value, created by setup rather than by another actor.
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := txCredentials.Create(ctx, newActor.ID, req.AdminUsername, req.AdminPassword); err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := txAudits.Create(ctx, audit.CreateParams{
			TenantID: newTenant.ID,
			ActorID:  newActor.ID,
			Action:   "setup.completed",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		if err := tx.Commit(); err != nil {
			if isSerializationFailure(err) {
				writeError(w, http.StatusConflict, codeConflict, "a concurrent setup request won the race")
				return
			}
			writeInternalError(w, err)
			return
		}

		token, err := issuer.Issue(identity.IssueRequest{
			TenantID:  newTenant.ID.String(),
			ActorID:   newActor.ID.String(),
			ActorType: identity.ActorHuman,
			Tier:      identity.TierAutonomous,
			TTL:       tokenTTL,
		}, identity.TierAutonomous)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, setupResponse{
			TenantID: newTenant.ID.String(),
			ActorID:  newActor.ID.String(),
			Token:    token,
		})
	})
}

// isSerializationFailure reports whether err is Postgres SQLSTATE 40001, the
// serialization_failure error a SERIALIZABLE transaction returns when it
// loses a conflicting concurrent transaction.
func isSerializationFailure(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "40001"
	}
	return false
}
