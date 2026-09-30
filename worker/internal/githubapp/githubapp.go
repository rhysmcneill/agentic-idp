// Package githubapp mints short-lived GitHub App installation tokens.
//
// One App, one private key, for the whole worker deployment — not per
// environment or per repo. The installation ID is resolved live from the
// target repo on every call rather than cached, the same way an AWS
// environment's role ARN is looked up fresh rather than assumed stable.
package githubapp

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/rhysmcneill/agentic-idp/pkg/ci"
)

const defaultBaseURL = "https://api.github.com"

// appJWTTTL bounds the App-level JWT used only to call the two endpoints
// below — GitHub caps this at 10 minutes; well inside that is plenty since
// it's used and discarded immediately.
const appJWTTTL = 9 * time.Minute

// Resolver mints installation-scoped GitHub API tokens from one App's
// credentials. Safe for concurrent use.
type Resolver struct {
	appID      string
	key        *rsa.PrivateKey
	baseURL    string
	httpClient *http.Client
}

// New constructs a Resolver for the App identified by appID, whose private
// key (the PEM GitHub generates when the App is registered) is privateKeyPEM.
func New(appID, privateKeyPEM string) (*Resolver, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("githubapp: parsing private key: %w", err)
	}
	return &Resolver{appID: appID, key: key, baseURL: defaultBaseURL, httpClient: http.DefaultClient}, nil
}

// appToken signs a short-lived JWT proving this Resolver's App identity —
// GitHub's required auth for both calls InstallationToken makes.
func (r *Resolver) appToken() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    r.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)), // GitHub rejects a token issued in what it sees as the future; a small backdate absorbs clock drift.
		ExpiresAt: jwt.NewNumericDate(now.Add(appJWTTTL)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(r.key)
	if err != nil {
		return "", fmt.Errorf("githubapp: signing app jwt: %w", err)
	}
	return token, nil
}

type githubError struct {
	Message string `json:"message"`
}

func (r *Resolver) do(ctx context.Context, method, path, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, r.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		var ghErr githubError
		_ = json.NewDecoder(resp.Body).Decode(&ghErr)
		if ghErr.Message == "" {
			ghErr.Message = resp.Status
		}
		return fmt.Errorf("github api: %s: %s", resp.Status, ghErr.Message)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// InstallationToken resolves repo's installation ID live, then mints a
// ~1-hour access token scoped to that installation — minted fresh on every
// call and never cached, matching MintCredentials' fail-closed, short-lived
// pattern for AWS.
func (r *Resolver) InstallationToken(ctx context.Context, repo string) (ci.Secret, error) {
	appJWT, err := r.appToken()
	if err != nil {
		return "", err
	}

	var installation struct {
		ID int64 `json:"id"`
	}
	if err := r.do(ctx, http.MethodGet, "/repos/"+repo+"/installation", appJWT, &installation); err != nil {
		return "", fmt.Errorf("githubapp: resolving installation for %q: %w", repo, err)
	}

	var access struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installation.ID)
	if err := r.do(ctx, http.MethodPost, path, appJWT, &access); err != nil {
		return "", fmt.Errorf("githubapp: minting installation token for %q: %w", repo, err)
	}

	return ci.Secret(access.Token), nil
}
