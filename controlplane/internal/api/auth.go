package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

type claimsContextKey struct{}

// claimsFromContext returns the verified Claims a preceding requireAuth call
// attached to the request, if any.
func claimsFromContext(ctx context.Context) (*identity.Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(*identity.Claims)
	return claims, ok
}

// requireAuth wraps next so it only runs for a request carrying a valid,
// unrevoked Bearer token, with the verified Claims attached to the request
// context. Rejects everything else with 401 — there is no anonymous access
// to anything this wraps.
func requireAuth(verifier *identity.Verifier, checker identity.RevocationChecker, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or malformed Authorization header")
			return
		}
		token := strings.TrimPrefix(header, prefix)

		claims, err := verifier.Verify(r.Context(), token, checker)
		if err != nil {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
