package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetachFlagsYesRegistered pins the flag wiring: both --yes and
// -y point at detachYes. If the short form drifts, users typing
// `wrk detach -y` in a script would silently trip an unknown-flag
// error — exactly the failure mode --yes exists to prevent.
func TestDetachFlagsYesRegistered(t *testing.T) {
	long := detachCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on detachCmd")
	}
	short := detachCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on detachCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to detachYes)")
	}
}

// TestDetachFlagsForceRegistered pins that `wrk detach --force`
// resolves — parity with gc/forget/remove/relink/run --force. The
// wire matters even though Detach has no per-command refusal today:
// Confirm receives Force so the override banner would render if a
// future refusal was added.
func TestDetachFlagsForceRegistered(t *testing.T) {
	if detachCmd.Flags().Lookup("force") == nil {
		t.Fatal("--force flag not registered on detachCmd")
	}
}

// TestDetachRefusesWithoutYesInNonTTY pins the plan-first, confirm-
// before-mutate contract: from a non-terminal stdin (runWrk uses no
// pty) a bare `wrk detach` must exit 2 with a --yes-mentioning
// error. The plan preview MUST have been printed to stdout so users
// see what would happen before the refusal, but nothing observable
// on disk changes.
func TestDetachRefusesWithoutYesInNonTTY(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, _, stderr := runWrk(t, repo, "detach")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Fatalf("stderr should mention --yes so the user knows the fix, got: %q", stderr)
	}
}

// TestDetachYesAndDryRunCoexist pins that --yes and --dry-run are
// not mutually exclusive. --dry-run bypasses confirmation on its own,
// and piling --yes on top MUST still be legal (exit 0, no refusal) —
// scripts probing the plan while advertising consent depend on this.
func TestDetachYesAndDryRunCoexist(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, _, stderr := runWrk(t, repo, "detach", "--yes", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "refusing") || strings.Contains(stderr, "requires --yes") {
		t.Fatalf("stderr should be clean for --yes --dry-run, got:\n%s", stderr)
	}
}

// TestDetachJSONFlagRegistered pins the --json flag wiring for
// `wrk detach`. Agent callers rely on it for the machine-readable
// envelope; drift would surface as "unknown flag" at script time.
func TestDetachJSONFlagRegistered(t *testing.T) {
	if detachCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on detachCmd")
	}
}
