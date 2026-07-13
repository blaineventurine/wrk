package main

import (
	"errors"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// TestGCJSONFlagRegistered pins the --json flag wiring for `wrk gc`.
// The flag is the agent-facing on-ramp to a machine-readable
// plan+result envelope.
func TestGCJSONFlagRegistered(t *testing.T) {
	if gcCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on gcCmd")
	}
}

// TestGCFlagsYesRegistered mirrors the parity check across every
// destructive command's --yes wiring; adding --json must not have
// broken the existing prompt-bypass path.
func TestGCFlagsYesRegistered(t *testing.T) {
	long := gcCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on gcCmd")
	}
	short := gcCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on gcCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to gcYes)")
	}
}

// TestGCExitCodeFlagRegistered pins the --exit-code flag wiring:
// scripts and CI can rely on it being present so `wrk gc --exit-code`
// stays a stable probe.
func TestGCExitCodeFlagRegistered(t *testing.T) {
	if gcCmd.Flags().Lookup("exit-code") == nil {
		t.Fatal("--exit-code flag not registered on gcCmd")
	}
}

// TestGCExitCodeSignalEmpty pins the "nothing to do" branch: an
// empty plan under --exit-code MUST exit 0 (nil) — the flag signals
// "there was cleanup to do", not "the command ran".
func TestGCExitCodeSignalEmpty(t *testing.T) {
	prev := gcExitCode
	defer func() { gcExitCode = prev }()
	gcExitCode = true

	err := gcExitCodeSignal(engine.GCPlan{})
	if err != nil {
		t.Fatalf("empty plan: expected nil, got %v", err)
	}
}

// TestGCExitCodeSignalNonEmpty pins the primary contract: a plan
// with anything to do returns the exitCode{code:1} sentinel when
// --exit-code is set. The Execute helper maps that to os.Exit(1)
// with silent stderr, matching `wrk status --exit-code`.
func TestGCExitCodeSignalNonEmpty(t *testing.T) {
	prev := gcExitCode
	defer func() { gcExitCode = prev }()
	gcExitCode = true

	plan := engine.GCPlan{Ghosts: []string{"/tmp/ghost"}}
	err := gcExitCodeSignal(plan)
	if err == nil {
		t.Fatal("expected exitCode sentinel, got nil")
	}
	var ec exitCode
	if !errors.As(err, &ec) {
		t.Fatalf("expected exitCode via errors.As, got %T: %v", err, err)
	}
	if ec.code != 1 {
		t.Errorf("code = %d, want 1", ec.code)
	}
}

// TestGCExitCodeSignalFlagOff pins that without --exit-code the
// helper always returns nil, even for a plan with work. The flag is
// opt-in — off means gc's exit contract is exactly 0/2.
func TestGCExitCodeSignalFlagOff(t *testing.T) {
	prev := gcExitCode
	defer func() { gcExitCode = prev }()
	gcExitCode = false

	plan := engine.GCPlan{Ghosts: []string{"/tmp/ghost"}}
	if err := gcExitCodeSignal(plan); err != nil {
		t.Fatalf("flag off: expected nil, got %v", err)
	}
}
