package main

import (
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
