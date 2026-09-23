// Package signingkey persists the control plane's own Ed25519 token-signing
// key so it survives restarts, generated once and never requiring an
// operator to supply or manage it — see docs/DECISIONS.md 018 for the same
// principle applied to the admin credential.
package signingkey

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/signingkey/sqlcgen"
	"github.com/rhysmcneill/agentic-idp/pkg/identity"
)

// LoadOrCreate returns the deployment's signing key, generating and
// persisting one if none exists yet. Safe to call from multiple instances
// starting concurrently: the schema's singleton constraint means only one
// INSERT can ever succeed, and a losing caller falls back to reading the
// row the winner just created.
func LoadOrCreate(ctx context.Context, db *sql.DB) (ed25519.PrivateKey, error) {
	q := sqlcgen.New(db)

	row, err := q.GetSigningKey(ctx)
	if err == nil {
		return ed25519.PrivateKey(row.PrivateKey), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("signingkey: loading: %w", err)
	}

	_, priv, err := identity.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("signingkey: generating: %w", err)
	}

	created, err := q.CreateSigningKey(ctx, priv)
	if err == nil {
		return ed25519.PrivateKey(created.PrivateKey), nil
	}

	// Another instance won the race to create the singleton row first —
	// read back what it wrote rather than treating this as a failure.
	row, getErr := q.GetSigningKey(ctx)
	if getErr != nil {
		return nil, fmt.Errorf("signingkey: creating: %w (and re-reading after conflict: %v)", err, getErr)
	}
	return ed25519.PrivateKey(row.PrivateKey), nil
}
