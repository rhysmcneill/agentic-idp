package signingkey_test

import (
	"context"
	"testing"

	"github.com/rhysmcneill/agentic-idp/controlplane/internal/dbtest"
	"github.com/rhysmcneill/agentic-idp/controlplane/internal/signingkey"
)

func TestLoadOrCreate_GeneratesOnce(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	first, err := signingkey.LoadOrCreate(ctx, conn)
	if err != nil {
		t.Fatalf("LoadOrCreate (first): %v", err)
	}
	if len(first) == 0 {
		t.Fatal("LoadOrCreate returned an empty key")
	}

	second, err := signingkey.LoadOrCreate(ctx, conn)
	if err != nil {
		t.Fatalf("LoadOrCreate (second): %v", err)
	}

	if string(first) != string(second) {
		t.Error("LoadOrCreate generated a different key on a second call — it should persist and reload the same one")
	}
}

func TestLoadOrCreate_UsableForSigning(t *testing.T) {
	conn := dbtest.New(t)
	ctx := context.Background()

	priv, err := signingkey.LoadOrCreate(ctx, conn)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(priv.Seed()) == 0 {
		t.Error("returned key has no seed — not a valid Ed25519 private key")
	}
}
