package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/credential"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// tokenTTL is how long an issued session token is valid before it must be
// re-minted via another POST /v1/token.
const tokenTTL = 8 * time.Hour

type tokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

func handlePostToken(actors *actor.Store, credentials *credential.Store, audits *audit.Store, issuer *identity.Issuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req tokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.Username == "" || req.Password == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "username and password are required")
			return
		}

		ctx := r.Context()

		actorID, err := credentials.Verify(ctx, req.Username, req.Password)
		if errors.Is(err, credential.ErrNotFound) || errors.Is(err, credential.ErrIncorrectPassword) {
			// Identical response for both: a login attempt must not be
			// usable to enumerate valid usernames.
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "incorrect username or password")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		act, err := actors.Get(ctx, actorID)
		if errors.Is(err, actor.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "incorrect username or password")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}
		if act.Status != actor.StatusActive {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "incorrect username or password")
			return
		}

		token, err := issuer.Issue(identity.IssueRequest{
			TenantID:  act.TenantID.String(),
			ActorID:   act.ID.String(),
			ActorType: act.Type,
			Tier:      act.TrustTier,
			TTL:       tokenTTL,
		}, act.TrustTier)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: act.TenantID,
			ActorID:  act.ID,
			Action:   "session.issued",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusOK, tokenResponse{Token: token})
	})
}
