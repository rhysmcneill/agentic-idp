package config_test

import (
	"testing"

	"github.com/rhysmcneill/agentic-idp/cli/internal/config"
)

// withTempConfigDir redirects os.UserConfigDir() into a temp directory for
// the duration of the test — HOME covers darwin's unconditional
// $HOME/Library/Application Support, and clearing XDG_CONFIG_HOME makes
// Linux fall back to $HOME/.config instead of honouring a CI-set value.
func withTempConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
}

func TestLoad_NothingSavedYet(t *testing.T) {
	withTempConfigDir(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != (config.Config{}) {
		t.Errorf("Load with nothing saved = %+v, want zero value", cfg)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	withTempConfigDir(t)

	want := config.Config{Server: "http://localhost:8080", Token: "a-token"}
	if err := config.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestSave_OverwritesPrevious(t *testing.T) {
	withTempConfigDir(t)

	if err := config.Save(config.Config{Server: "http://old", Token: "old-token"}); err != nil {
		t.Fatalf("Save (first): %v", err)
	}
	want := config.Config{Server: "http://new", Token: "new-token"}
	if err := config.Save(want); err != nil {
		t.Fatalf("Save (second): %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}
