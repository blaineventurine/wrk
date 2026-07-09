package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunCmdRegistered pins the wiring: `wrk run` must be reachable
// off the root command. A regression here would surface as `unknown
// command "run"` to users, which is exactly the failure that made this
// task worth wiring.
func TestRunCmdRegistered(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "run" {
			return
		}
	}
	t.Fatal("run command not registered on root")
}

// TestRunCmdRequiresExactlyOneArg pins cobra.ExactArgs(1): zero or two
// positional args must error before RunE ever runs (so we never call
// engine.Run with an empty name and get a less-specific error). Cobra
// resolves this at parse time via runCmd.Args; call it directly rather
// than invoke Execute() so the assertion isolates argument validation.
func TestRunCmdRequiresExactlyOneArg(t *testing.T) {
	if err := runCmd.Args(runCmd, []string{}); err == nil {
		t.Error("expected error for 0 args")
	}
	if err := runCmd.Args(runCmd, []string{"node"}); err != nil {
		t.Errorf("unexpected error for 1 arg: %v", err)
	}
	if err := runCmd.Args(runCmd, []string{"node", "env"}); err == nil {
		t.Error("expected error for 2 args")
	}
}

// TestRunCmdDryRunFlagRegistered pins the --dry-run wiring. The
// package-global `dryRun` is shared across commands, so if someone
// removes the per-command registration in a future refactor, `wrk run
// --dry-run` would silently become an unknown-flag error.
func TestRunCmdDryRunFlagRegistered(t *testing.T) {
	if runCmd.Flags().Lookup("dry-run") == nil {
		t.Fatal("--dry-run flag not registered on runCmd")
	}
}

// TestRunFlagsYesRegistered pins the flag wiring for `wrk run --yes`
// / `-y`. A future refactor that dropped the short form would still
// pass every other test in this file — this one exists so that class
// of drift is caught at build+test time.
func TestRunFlagsYesRegistered(t *testing.T) {
	long := runCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on runCmd")
	}
	short := runCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on runCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to runYes)")
	}
}

// TestRunFlagsForceRegistered pins `wrk run --force` — parity with
// the other destructive commands. Run has no soft refusal today, so
// --force behaves as a stronger --yes; the flag still exists so
// scripts that pass --force across all destructive commands do not
// need a special-case for run.
func TestRunFlagsForceRegistered(t *testing.T) {
	if runCmd.Flags().Lookup("force") == nil {
		t.Fatal("--force flag not registered on runCmd")
	}
}

// TestRunRefusesUnknownResourceExitsTwo pins that the RunE surfaces
// the engine's "not configured" error to stderr with exit code 2.
// The confirm path is never reached because BuildRunPlan errors
// first — this test guards that the plan builder still runs before
// Confirm asks anything.
func TestRunRefusesUnknownResourceExitsTwo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, _, stderr := runWrk(t, repo, "run", "nope", "--yes")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, `"nope" not configured`) {
		t.Fatalf("stderr should surface the engine's unknown-resource error, got: %q", stderr)
	}
}
