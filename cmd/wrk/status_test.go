package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// TestHasProblemsProblemStates confirms every state that `wrk link`
// would fix is treated as a problem. This includes both actionable
// failures (conflict, stale, absent) and the "fresh checkout" states
// (pending, missing, not-linked) that a first-run link resolves.
func TestHasProblemsProblemStates(t *testing.T) {
	for _, s := range []engine.State{
		engine.StateConflict,
		engine.StateStale,
		engine.StateAbsent,
		engine.StatePending,
		engine.StateMissing,
		engine.StateNotLinked,
	} {
		t.Run(string(s), func(t *testing.T) {
			rows := []engine.ResourceStatus{{State: s}}
			if !hasProblems(rows) {
				t.Fatalf("hasProblems(%s) = false, want true", s)
			}
		})
	}
}

// TestHasProblemsIntentionalStates confirms that intentional states
// (linked, detached, expected) are NOT reported as problems — they
// represent a workspace that is either healthy or deliberately opted
// out of shared storage.
func TestHasProblemsIntentionalStates(t *testing.T) {
	for _, s := range []engine.State{
		engine.StateLinked,
		engine.StateDetached,
		engine.StateExpected,
	} {
		t.Run(string(s), func(t *testing.T) {
			rows := []engine.ResourceStatus{{State: s}}
			if hasProblems(rows) {
				t.Fatalf("hasProblems(%s) = true, want false", s)
			}
		})
	}
}

// TestHasProblemsEmpty confirms an empty status report is not a
// problem — no resources means nothing needs attention.
func TestHasProblemsEmpty(t *testing.T) {
	if hasProblems(nil) {
		t.Fatal("hasProblems(nil) = true, want false")
	}
}

// TestHasProblemsMixedReportsFirstProblem confirms a mixed set of
// states triggers when any single row is problematic — the exit-code
// flag should fire on the first problem.
func TestHasProblemsMixedReportsFirstProblem(t *testing.T) {
	rows := []engine.ResourceStatus{
		{State: engine.StateLinked},
		{State: engine.StateExpected},
		{State: engine.StateMissing}, // this one is a problem
		{State: engine.StateLinked},
	}
	if !hasProblems(rows) {
		t.Fatal("hasProblems(mixed with missing) = false, want true")
	}
}

// TestExitCodeRoundTripsThroughErrorsAs pins U4: the exitCode sentinel
// MUST survive being wrapped in another error via fmt.Errorf("%w",…).
// If it doesn't, Execute() falls through to the generic error path
// (exit 2 + stderr) instead of surfacing the intended exit signal
// (silent exit 1). Wrappings happen every time a middle layer adds
// context, so this invariant is load-bearing for `wrk status
// --exit-code` in a real call graph.
func TestExitCodeRoundTripsThroughErrorsAs(t *testing.T) {
	original := exitCode{code: 1}

	wrapped := fmt.Errorf("something happened: %w", original)

	var got exitCode
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As failed to recover exitCode from %v", wrapped)
	}
	if got.code != 1 {
		t.Fatalf("recovered code = %d, want 1", got.code)
	}
}

// TestExitCodeDoubleWrapStillRoundTrips confirms the invariant holds
// through nested wrapping too — real call graphs stack multiple
// wrapContext levels. errors.As must still unwrap the sentinel and
// yield the exit code intact.
func TestExitCodeDoubleWrapStillRoundTrips(t *testing.T) {
	inner := exitCode{code: 1}
	wrapped := fmt.Errorf("first: %w", fmt.Errorf("second: %w", inner))

	var got exitCode
	if !errors.As(wrapped, &got) {
		t.Fatalf("errors.As failed to recover exitCode through two wrappers: %v", wrapped)
	}
	if got.code != 1 {
		t.Fatalf("code through double wrap = %d, want 1", got.code)
	}
}

// TestExitCodeIsSilentByDesign pins the "no message on stderr" side
// of the sentinel: Error() must return "" so the top-level Execute
// can distinguish the exit-signal path (silent) from a real error
// (loud). If someone gives exitCode a non-empty Error() the
// --exit-code contract breaks — status would print junk to stderr
// on every pre-commit hook.
func TestExitCodeIsSilentByDesign(t *testing.T) {
	if msg := (exitCode{code: 1}).Error(); msg != "" {
		t.Fatalf("exitCode.Error() = %q, want empty string", msg)
	}
}

// TestShortFingerprintTruncation pins the boundary behaviour of
// short() — it is what the FINGERPRINT column depends on. An empty
// input yields "-", a short-enough input passes through, and a long
// input is truncated to exactly 12 characters (the width the columns
// are laid out for).
func TestShortFingerprintTruncation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "-"},
		{"abc", "abc"},
		{strings.Repeat("f", 12), strings.Repeat("f", 12)},
		{"abcdef012345Z", "abcdef012345"},
		{strings.Repeat("f", 64), strings.Repeat("f", 12)},
	}
	for _, c := range cases {
		if got := short(c.in); got != c.want {
			t.Errorf("short(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHasNonSharedOriginDetection confirms the ORIGIN-column gate
// used by printStatus fires only when at least one row is non-shared.
// This helper drives whether the status table gains an extra column,
// so its truth table is worth pinning explicitly.
func TestHasNonSharedOriginDetection(t *testing.T) {
	cases := []struct {
		name string
		rows []engine.ResourceStatus
		want bool
	}{
		{"empty", nil, false},
		{"all shared", []engine.ResourceStatus{{Origin: "shared"}, {Origin: "shared"}}, false},
		{"one local", []engine.ResourceStatus{{Origin: "shared"}, {Origin: "local"}}, true},
		{"one override", []engine.ResourceStatus{{Origin: "shared"}, {Origin: "local-override"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasNonSharedOrigin(c.rows); got != c.want {
				t.Errorf("hasNonSharedOrigin = %v, want %v", got, c.want)
			}
		})
	}
}
