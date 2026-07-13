package engine

import "testing"

// rollupCases is the shared truth table for the two rollup
// implementations: the human one (workspaces.go rollup, feeding
// `wrk workspaces` / `wrk status` summaries) and the JSON one
// (jsonoutput.go rollupState, feeding `wrk status --json`). Both
// collapse a workspace's per-resource states into a single label and
// MUST agree — they render the same workspace to the same user, and a
// divergence would show two different health verdicts for identical
// state vectors. TestRollupStateAgreesWithHumanRollup iterates this
// same table.
var rollupCases = []struct {
	name   string
	counts map[State]int
	want   WorkspaceState
}{
	{"empty", map[State]int{}, WorkspaceEmpty},

	{"all linked", map[State]int{StateLinked: 3}, WorkspaceLinked},
	{
		"linked + expected still linked",
		map[State]int{StateLinked: 2, StateExpected: 1},
		WorkspaceLinked,
	},

	{"all detached", map[State]int{StateDetached: 2}, WorkspaceDetached},
	{
		"mix of linked and detached",
		map[State]int{StateLinked: 1, StateDetached: 1},
		WorkspacePartial,
	},

	// Isolated is a resting state on par with linked/detached: a
	// workspace whose every resource is pinned to a private variant
	// rolls up as "isolated", and any mix with the other resting
	// families is a deliberate partial — never unhealthy, never
	// silently folded into linked.
	{"all isolated", map[State]int{StateIsolated: 2}, WorkspaceIsolated},
	{
		"isolated + linked is partial",
		map[State]int{StateIsolated: 1, StateLinked: 1},
		WorkspacePartial,
	},
	{
		"isolated + expected is partial",
		map[State]int{StateIsolated: 1, StateExpected: 1},
		WorkspacePartial,
	},
	{
		"isolated + detached is partial",
		map[State]int{StateIsolated: 1, StateDetached: 1},
		WorkspacePartial,
	},
	{
		"linked + detached + isolated is partial",
		map[State]int{StateLinked: 1, StateDetached: 1, StateIsolated: 1},
		WorkspacePartial,
	},
	{
		"isolated + conflict is unhealthy",
		map[State]int{StateIsolated: 1, StateConflict: 1},
		WorkspaceUnhealthy,
	},
	{
		"isolated + pending is pending",
		map[State]int{StateIsolated: 1, StatePending: 1},
		WorkspacePending,
	},

	{
		"any pending wins over healthy",
		map[State]int{StateLinked: 2, StatePending: 1},
		WorkspacePending,
	},

	{
		"unhealthy dominates everything",
		map[State]int{StateLinked: 5, StatePending: 1, StateConflict: 1},
		WorkspaceUnhealthy,
	},
	{
		"stale is unhealthy",
		map[State]int{StateLinked: 1, StateStale: 1},
		WorkspaceUnhealthy,
	},
	{
		"not-linked is unhealthy",
		map[State]int{StateNotLinked: 1},
		WorkspaceUnhealthy,
	},
	{
		"absent is unhealthy",
		map[State]int{StateAbsent: 1},
		WorkspaceUnhealthy,
	},

	// H7: Missing counted as unhealthy. A fresh-checkout workspace
	// whose shared exists but has never been linked into it renders
	// every row red in per-resource `wrk status`; the rollup MUST
	// match rather than falsely flagging WorkspaceLinked.
	{
		"all missing is unhealthy",
		map[State]int{StateMissing: 3},
		WorkspaceUnhealthy,
	},
	{
		"mixed linked + missing is unhealthy",
		map[State]int{StateLinked: 2, StateMissing: 1},
		WorkspaceUnhealthy,
	},
	{
		"missing beats pending in the priority order",
		map[State]int{StateMissing: 1, StatePending: 1},
		WorkspaceUnhealthy,
	},
}

// expandStates flattens a count vector into the []State slice
// rollupState consumes. Order is irrelevant: both rollups are
// order-independent aggregations.
func expandStates(counts map[State]int) []State {
	states := []State{}
	for s, n := range counts {
		for range n {
			states = append(states, s)
		}
	}
	return states
}

func TestRollup(t *testing.T) {
	for _, tc := range rollupCases {
		t.Run(tc.name, func(t *testing.T) {
			// rollup takes rows only to check emptiness; a slice of the same
			// length as the total counts is sufficient.
			rows := make([]ResourceStatus, len(expandStates(tc.counts)))

			if got := rollup(rows, tc.counts); got != tc.want {
				t.Fatalf("rollup = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRollupStateAgreesWithHumanRollup pins the human/JSON agreement:
// for every count vector in the shared table, jsonoutput.go's
// rollupState emits the exact string the human rollup labels the
// workspace with. `wrk status` and `wrk status --json` describe the
// same workspace — a divergence (e.g. rollupState forgetting the
// isolated family) would let a dashboard call a workspace "partial"
// while the terminal calls it "isolated".
func TestRollupStateAgreesWithHumanRollup(t *testing.T) {
	for _, tc := range rollupCases {
		t.Run(tc.name, func(t *testing.T) {
			states := expandStates(tc.counts)
			rows := make([]ResourceStatus, len(states))

			human := rollup(rows, tc.counts)
			machine := rollupState(states)

			if string(human) != machine {
				t.Fatalf("human rollup %q != JSON rollupState %q for counts %v",
					human, machine, tc.counts)
			}
			if machine != string(tc.want) {
				t.Fatalf("rollupState = %q, want %q", machine, tc.want)
			}
		})
	}
}
