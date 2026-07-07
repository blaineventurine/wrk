package engine

import "testing"

func TestRollup(t *testing.T) {
	cases := []struct {
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// rollup takes rows only to check emptiness; a slice of the same
			// length as the total counts is sufficient.
			total := 0
			for _, n := range tc.counts {
				total += n
			}
			rows := make([]ResourceStatus, total)

			if got := rollup(rows, tc.counts); got != tc.want {
				t.Fatalf("rollup = %q, want %q", got, tc.want)
			}
		})
	}
}
