// Package githuboidc implements ciauth.Verifier for GitHub Actions OIDC
// ID tokens.
package githuboidc

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
	"github.com/rhysmcneill/agentic-idp/worker/internal/ciauth"
)

const defaultIssuerURL = "https://token.actions.githubusercontent.com"

// Verifier checks GitHub Actions OIDC ID tokens via standard OIDC discovery
// against issuerURL; JWKS fetch, cache and key rotation are handled by oidc.Provider.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

// New constructs a Verifier expecting audience as the token's aud claim.
func New(ctx context.Context, audience string) (*Verifier, error) {
	return newWithIssuer(ctx, defaultIssuerURL, audience)
}

func newWithIssuer(ctx context.Context, issuerURL, audience string) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("githubactions: discovering oidc provider: %w", err)
	}
	return &Verifier{verifier: provider.Verifier(&oidc.Config{ClientID: audience})}, nil
}

// Provider implements ciauth.Verifier.
func (v *Verifier) Provider() ci.Provider { return ci.ProviderGitHubActions }

// githubClaims is the subset of GitHub's Actions OIDC token this Verifier reads.
type githubClaims struct {
	Repository  string `json:"repository"`
	WorkflowRef string `json:"workflow_ref"`
	Ref         string `json:"ref"`
	RunID       string `json:"run_id"`
	Actor       string `json:"actor"`
}

// Verify implements ciauth.Verifier.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (ciauth.Claims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return ciauth.Claims{}, fmt.Errorf("githubactions: verifying token: %w", err)
	}

	var claims githubClaims
	if err := idToken.Claims(&claims); err != nil {
		return ciauth.Claims{}, fmt.Errorf("githubactions: decoding claims: %w", err)
	}

	return ciauth.Claims{
		Repo:        claims.Repository,
		WorkflowRef: parseWorkflowRef(claims.Repository, claims.WorkflowRef),
		Ref:         claims.Ref,
		RunID:       claims.RunID,
		Actor:       claims.Actor,
		URL:         fmt.Sprintf("https://github.com/%s/actions/runs/%s", claims.Repository, claims.RunID),
	}, nil
}

// parseWorkflowRef strips GitHub's "{repo}/{path}@{ref}" workflow_ref claim
// down to just the path, matching pipeline.Pipeline.WorkflowRef's format.
func parseWorkflowRef(repo, workflowRef string) string {
	if i := strings.IndexByte(workflowRef, '@'); i >= 0 {
		workflowRef = workflowRef[:i]
	}
	return strings.TrimPrefix(workflowRef, repo+"/")
}
