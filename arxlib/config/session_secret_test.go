package config

import (
	"os"
	"testing"
)

// chdirTemp switches into a fresh temp dir for the duration of the test so
// resolveSessionSecret's SaveLocal writes land in an isolated config/local.json.
func chdirTemp(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

func TestResolveSessionSecret_EnvWins(t *testing.T) {
	chdirTemp(t)
	t.Setenv("SESSION_SECRET", "from-env")

	if got := resolveSessionSecret(&LocalConfig{SessionSecret: "from-local"}); got != "from-env" {
		t.Fatalf("env should win: got %q", got)
	}
}

func TestResolveSessionSecret_UsesPersistedLocal(t *testing.T) {
	chdirTemp(t)
	os.Unsetenv("SESSION_SECRET")

	if got := resolveSessionSecret(&LocalConfig{SessionSecret: "from-local"}); got != "from-local" {
		t.Fatalf("local secret should be used: got %q", got)
	}
}

func TestResolveSessionSecret_GeneratesAndPersists(t *testing.T) {
	chdirTemp(t)
	os.Unsetenv("SESSION_SECRET")

	local := &LocalConfig{}
	secret := resolveSessionSecret(local)

	if secret == "" || secret == "change-me-in-production" {
		t.Fatalf("expected a generated non-default secret, got %q", secret)
	}
	if local.SessionSecret != secret {
		t.Fatalf("secret not written back to the struct: %q vs %q", local.SessionSecret, secret)
	}

	// It must have been persisted so the key is stable across restarts.
	reloaded, err := LoadLocal()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SessionSecret != secret {
		t.Fatalf("secret not persisted: reloaded %q, want %q", reloaded.SessionSecret, secret)
	}

	// A second resolve from the reloaded config returns the same key, not a new one.
	if got := resolveSessionSecret(reloaded); got != secret {
		t.Fatalf("second resolve regenerated the secret: got %q, want %q", got, secret)
	}
}

func TestResolveSessionSecret_NilLocalDoesNotPanicOrPersist(t *testing.T) {
	chdirTemp(t)
	os.Unsetenv("SESSION_SECRET")

	secret := resolveSessionSecret(nil)
	if secret == "" {
		t.Fatal("expected an ephemeral secret for nil local, got empty")
	}
	// Nothing should have been written when local.json was unreadable (nil).
	if _, err := os.Stat("config/local.json"); !os.IsNotExist(err) {
		t.Fatalf("nil local should not persist a config file (stat err=%v)", err)
	}
}
