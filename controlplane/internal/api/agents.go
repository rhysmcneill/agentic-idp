package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/actor"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/audit"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/environment"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/team"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// maxAgentTTL bounds how long an agent's token can be valid for at mint
// time — "indefinite agent credentials are not offered" (AGENT-MODEL.md).
const maxAgentTTL = 30 * 24 * time.Hour

var tierNames = map[string]identity.Tier{
	"read_only":         identity.TierReadOnly,
	"human_in_the_loop": identity.TierHumanInTheLoop,
	"autonomous":        identity.TierAutonomous,
}

type createAgentRequest struct {
	Name         string   `json:"name"`
	Team         string   `json:"team"`
	Tier         string   `json:"tier"`
	Environments []string `json:"environments"`
	TTLSeconds   int64    `json:"ttl_seconds"`

	// IdempotencyKey, when set, makes a retried call with the same key
	// return the actor the first call created rather than minting a second
	// one. The reissued token is not byte-identical to the original — agent
	// tokens are never persisted, so nothing exists to replay — but the
	// underlying actor is the same one, not a duplicate.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type createAgentResponse struct {
	ActorID string `json:"actor_id"`
	Token   string `json:"token"`
}

func handlePostAgents(teams *team.Store, environments *environment.Store, actors *actor.Store, audits *audit.Store, issuer *identity.Issuer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeInternalError(w, errors.New("agents: no claims in request context — requireAuth was not applied"))
			return
		}
		// Recursive delegation launders authority: if agent A can mint
		// agent B, the delegation chain becomes a place to hide rather
		// than a record.
		if claims.ActorType == identity.ActorAgent {
			writeError(w, http.StatusForbidden, codeForbidden, "agents may not enrol agents")
			return
		}

		var req createAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "malformed request body")
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "name is required")
			return
		}
		if req.Team == "" {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "team is required")
			return
		}
		tier, ok := tierNames[req.Tier]
		if !ok {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "tier must be one of read_only, human_in_the_loop, autonomous")
			return
		}
		if req.TTLSeconds <= 0 {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "ttl_seconds is required and must be positive")
			return
		}
		ttl := time.Duration(req.TTLSeconds) * time.Second
		if ttl > maxAgentTTL {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("ttl_seconds must not exceed %d", int64(maxAgentTTL.Seconds())))
			return
		}

		// An actor cannot grant a tier higher than its own — checked here,
		// before any row is written, not left to Issue to catch afterward;
		// see identity.Issue's own issuerTier check for the same rule
		// applied again at token-mint time.
		if tier > claims.Tier {
			writeError(w, http.StatusForbidden, codeForbidden, "cannot grant a tier higher than your own")
			return
		}

		tenantID, err := uuid.Parse(claims.TenantID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("agents: parsing tenant id from claims: %w", err))
			return
		}
		authorizedBy, err := uuid.Parse(claims.ActorID)
		if err != nil {
			writeInternalError(w, fmt.Errorf("agents: parsing actor id from claims: %w", err))
			return
		}

		ctx := r.Context()

		if req.IdempotencyKey != "" {
			existing, err := actors.GetByIdempotencyKey(ctx, tenantID, authorizedBy, req.IdempotencyKey)
			if err != nil && !errors.Is(err, actor.ErrNotFound) {
				writeInternalError(w, err)
				return
			}
			if err == nil {
				// Re-run the same authority checks the create path applies,
				// rather than trusting that existing's stored tier/scope
				// still matches the caller's current authority — nothing
				// can change either today, but a reissue must never become
				// the one path that skips this check once something can.
				if existing.TrustTier > claims.Tier {
					writeError(w, http.StatusForbidden, codeForbidden, "cannot grant a tier higher than your own")
					return
				}
				existingEnvIDs, err := actors.ListEnvironments(ctx, existing.ID)
				if err != nil {
					writeInternalError(w, err)
					return
				}
				if claims.Delegation != nil {
					for _, envID := range existingEnvIDs {
						if !claims.PermitsEnvironment(envID.String()) {
							writeError(w, http.StatusForbidden, codeForbidden, "cannot grant an environment outside your own scope")
							return
						}
					}
				}

				token, err := reissueAgentToken(issuer, claims, existing, existingEnvIDs)
				if err != nil {
					writeInternalError(w, err)
					return
				}
				if _, err := audits.Create(ctx, audit.CreateParams{
					TenantID: tenantID,
					ActorID:  authorizedBy,
					Action:   "agent.token.reissued",
				}); err != nil {
					writeInternalError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, createAgentResponse{ActorID: existing.ID.String(), Token: token})
				return
			}
		}

		tm, err := teams.GetByName(ctx, tenantID, req.Team)
		if errors.Is(err, team.ErrNotFound) {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "unknown team")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		envIDs := make([]uuid.UUID, 0, len(req.Environments))
		for _, name := range req.Environments {
			env, err := environments.GetByName(ctx, tenantID, name)
			if errors.Is(err, environment.ErrNotFound) {
				writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf("unknown environment %q", name))
				return
			}
			if err != nil {
				writeInternalError(w, err)
				return
			}
			// Same "cannot grant more than you have" rule as tier, applied
			// to environment scope. The root actor (no Delegation) is exempt.
			if claims.Delegation != nil && !claims.PermitsEnvironment(env.ID.String()) {
				writeError(w, http.StatusForbidden, codeForbidden, fmt.Sprintf("cannot grant environment %q outside your own scope", name))
				return
			}
			envIDs = append(envIDs, env.ID)
		}

		var idempotencyKey *string
		if req.IdempotencyKey != "" {
			idempotencyKey = &req.IdempotencyKey
		}

		expiresAt := time.Now().Add(ttl)
		newActor, err := actors.Create(ctx, actor.CreateParams{
			TenantID:       tenantID,
			Type:           identity.ActorAgent,
			Name:           req.Name,
			TeamID:         tm.ID,
			TrustTier:      tier,
			AuthorizedBy:   &authorizedBy,
			ExpiresAt:      &expiresAt,
			IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			writeInternalError(w, err)
			return
		}

		envIDStrings := make([]string, 0, len(envIDs))
		for _, envID := range envIDs {
			if err := actors.GrantEnvironment(ctx, newActor.ID, envID); err != nil {
				writeInternalError(w, err)
				return
			}
			envIDStrings = append(envIDStrings, envID.String())
		}

		token, err := issuer.Issue(identity.IssueRequest{
			TenantID:     claims.TenantID,
			ActorID:      newActor.ID.String(),
			ActorType:    identity.ActorAgent,
			Tier:         tier,
			Environments: envIDStrings,
			Delegation:   &identity.Delegation{AuthorizedBy: claims.ActorID, TeamID: tm.ID.String()},
			TTL:          ttl,
		}, claims.Tier)
		if err != nil {
			if errors.Is(err, identity.ErrPrivilegeEscalation) {
				writeError(w, http.StatusForbidden, codeForbidden, "cannot grant a tier higher than your own")
				return
			}
			writeInternalError(w, err)
			return
		}

		if _, err := audits.Create(ctx, audit.CreateParams{
			TenantID: tenantID,
			ActorID:  authorizedBy,
			Action:   "agent.enrolled",
		}); err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, http.StatusCreated, createAgentResponse{ActorID: newActor.ID.String(), Token: token})
	})
}

// reissueAgentToken mints a fresh token for an agent an earlier call already
// created, found via idempotency key — not a byte-identical replay (agent
// tokens are never persisted), but the same actor, not a duplicate.
func reissueAgentToken(issuer *identity.Issuer, claims *identity.Claims, existing actor.Actor, environmentIDs []uuid.UUID) (string, error) {
	envIDStrings := make([]string, len(environmentIDs))
	for i, id := range environmentIDs {
		envIDStrings[i] = id.String()
	}

	var ttl time.Duration
	if existing.ExpiresAt != nil {
		ttl = time.Until(*existing.ExpiresAt)
	}

	token, err := issuer.Issue(identity.IssueRequest{
		TenantID:     claims.TenantID,
		ActorID:      existing.ID.String(),
		ActorType:    identity.ActorAgent,
		Tier:         existing.TrustTier,
		Environments: envIDStrings,
		Delegation:   &identity.Delegation{AuthorizedBy: claims.ActorID, TeamID: existing.TeamID.String()},
		TTL:          ttl,
	}, claims.Tier)
	if err != nil {
		return "", fmt.Errorf("agents: reissuing token for idempotent replay: %w", err)
	}
	return token, nil
}
