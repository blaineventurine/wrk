package main

import (
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// TestRemoveJSONFlagRegistered pins the --json flag wiring for
// `wrk remove`.
func TestRemoveJSONFlagRegistered(t *testing.T) {
	if removeCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on removeCmd")
	}
}

// TestRemoveFlagsYesRegistered mirrors the parity check across
// every destructive command's --yes wiring.
func TestRemoveFlagsYesRegistered(t *testing.T) {
	long := removeCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on removeCmd")
	}
	short := removeCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on removeCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to removeYes)")
	}
}

// TestRemoveFlagsForceRegistered pins that --force still resolves
// after the --json addition — the two flags must coexist so agents
// pairing --json --force can override refusals in one call.
func TestRemoveFlagsForceRegistered(t *testing.T) {
	if removeCmd.Flags().Lookup("force") == nil {
		t.Fatal("--force flag not registered on removeCmd")
	}
}

// TestRunRemoveJSONGitBackendReportsPlanBytes pins the bytesFreed
// fallback: `git worktree remove` deletes inside git's own process, so
// wrk's Progress callback never fires and the measured total stays 0
// even after a successful multi-gigabyte removal. On the git backend a
// successful execute must report the plan's pre-computed TotalBytes
// instead of a misleading 0.
func TestRunRemoveJSONGitBackendReportsPlanBytes(t *testing.T) {
	plan := engine.RemovePlan{Backend: "git", TotalBytes: 1234}
	if got := effectiveBytesFreed(plan, true, 0); got != 1234 {
		t.Errorf("git backend, attempted, measured 0: got %d, want plan.TotalBytes 1234", got)
	}

	// Not attempted (dry-run / refused): nothing was freed — the plan
	// total must NOT leak into the result.
	if got := effectiveBytesFreed(plan, false, 0); got != 0 {
		t.Errorf("git backend, not attempted: got %d, want 0", got)
	}

	// jj sweeps file-by-file through Progress; its measured total is
	// authoritative and must pass through untouched.
	jjPlan := engine.RemovePlan{Backend: "jj", TotalBytes: 1234}
	if got := effectiveBytesFreed(jjPlan, true, 987); got != 987 {
		t.Errorf("jj backend keeps measured total: got %d, want 987", got)
	}
	if got := effectiveBytesFreed(jjPlan, true, 0); got != 0 {
		t.Errorf("jj backend, measured 0: got %d, want 0 (no fallback on jj)", got)
	}
}
