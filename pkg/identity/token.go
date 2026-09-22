package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// GenerateKeyPair creates a new Ed25519 signing key. The private key stays
// with whatever issues tokens (the control plane); the public key is
// distributed to anything that needs to verify them (the worker, the MCP
// server).
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("identity: generating key pair: %w", err)
	}
	return pub, priv, nil
}

type jwtClaims struct {
	jwt.RegisteredClaims
	TenantID     string      `json:"tenant_id"`
	ActorType    ActorType   `json:"actor_type"`
	Tier         Tier        `json:"tier"`
	Environments []string    `json:"environments"`
	Delegation   *Delegation `json:"delegation,omitempty"`
}

// Issuer signs tokens. It is only ever constructed by the control plane.
type Issuer struct {
	key ed25519.PrivateKey
}

// NewIssuer constructs an Issuer from a private signing key.
func NewIssuer(key ed25519.PrivateKey) *Issuer { return &Issuer{key: key} }

// Issue mints a token for req. issuerTier is the authority of whoever is
// requesting the mint — an actor cannot grant a tier higher than its own; see
// ErrPrivilegeEscalation and docs/AGENT-MODEL.md.
func (i *Issuer) Issue(req IssueRequest, issuerTier Tier) (string, error) {
	if req.TenantID == "" || req.ActorID == "" {
		return "", fmt.Errorf("%w: tenant and actor id are required", ErrInvalidRequest)
	}
	if !req.Tier.Valid() {
		return "", fmt.Errorf("%w: invalid tier %d", ErrInvalidRequest, req.Tier)
	}
	if req.TTL <= 0 {
		return "", fmt.Errorf("%w: ttl must be positive", ErrInvalidRequest)
	}
	if req.Tier > issuerTier {
		return "", ErrPrivilegeEscalation
	}

	now := time.Now()
	claims := jwtClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			Subject:   req.ActorID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(req.TTL)),
		},
		TenantID:     req.TenantID,
		ActorType:    req.ActorType,
		Tier:         req.Tier,
		Environments: req.Environments,
		Delegation:   req.Delegation,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(i.key)
	if err != nil {
		return "", fmt.Errorf("identity: signing token: %w", err)
	}
	return signed, nil
}

// Verifier checks tokens. It holds only the public key, so a compromised
// verifier (the worker, the MCP server) cannot mint tokens itself.
type Verifier struct {
	key ed25519.PublicKey
}

// NewVerifier constructs a Verifier from a public key.
func NewVerifier(key ed25519.PublicKey) *Verifier { return &Verifier{key: key} }

// Verify returns the Claims embedded in a signed token, or an error. This is
// the only path by which a Claims value comes into existence outside of Issue
// — there is no constructor that accepts caller-supplied claims directly.
//
// checker is consulted after signature and expiry both check out, so a
// revoked session or a whole-identity-revoked actor is rejected even with an
// otherwise valid token — see RevocationChecker.
func (v *Verifier) Verify(ctx context.Context, tokenString string, checker RevocationChecker) (*Claims, error) {
	if checker == nil {
		return nil, fmt.Errorf("identity: verify: a RevocationChecker is required")
	}

	var claims jwtClaims
	_, err := jwt.ParseWithClaims(tokenString, &claims, func(_ *jwt.Token) (any, error) {
		return v.key, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}))

	if errors.Is(err, jwt.ErrTokenExpired) {
		return nil, ErrTokenExpired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}

	revoked, err := checker.IsRevoked(ctx, claims.Subject, claims.ID)
	if err != nil {
		return nil, fmt.Errorf("identity: verify: checking revocation: %w", err)
	}
	if revoked {
		return nil, ErrTokenRevoked
	}

	return &Claims{
		TenantID:     claims.TenantID,
		ActorID:      claims.Subject,
		Jti:          claims.ID,
		ActorType:    claims.ActorType,
		Tier:         claims.Tier,
		Environments: claims.Environments,
		Delegation:   claims.Delegation,
		IssuedAt:     claims.IssuedAt.Time,
		ExpiresAt:    claims.ExpiresAt.Time,
	}, nil
}
