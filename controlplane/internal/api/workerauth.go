package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/workercred"
)

type workerCredentialContextKey struct{}

// workerCredentialFromContext returns the verified workercred.Credential a
// preceding requireWorkerAuth call attached to the request, if any.
func workerCredentialFromContext(ctx context.Context) (*workercred.Credential, bool) {
	cred, ok := ctx.Value(workerCredentialContextKey{}).(*workercred.Credential)
	return cred, ok
}

// requireWorkerAuth wraps next so it only runs for a request carrying a
// valid, unrevoked worker credential. Entirely separate from requireAuth: a
// worker never decides or takes a governed action, only executes jobs
// another actor already authorised, so it carries no Tier, Team or
// Delegation — its credential is verified by a hash lookup here, never by
// identity.Verifier.
func requireWorkerAuth(credentials *workercred.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or malformed Authorization header")
			return
		}
		token := strings.TrimPrefix(header, prefix)

		cred, err := credentials.Verify(r.Context(), token)
		if errors.Is(err, workercred.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "invalid or revoked worker credential")
			return
		}
		if err != nil {
			writeInternalError(w, err)
			return
		}

		ctx := context.WithValue(r.Context(), workerCredentialContextKey{}, &cred)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
