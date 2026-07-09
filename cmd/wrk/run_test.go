package main

import (
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
