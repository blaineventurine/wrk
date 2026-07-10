package main

import "testing"

// TestNewFlagBaseRegistered pins that `wrk new` exposes `--base` with
// an empty default: a user typing `wrk new feature --base main` gets
// their argument threaded through to engine.NewWorkspace, and a user
// who omits the flag gets the legacy behaviour (empty base means
// "fork off the invoking worktree's HEAD/@"). A regression that
// swapped StringVar for a different type or moved the flag registration
// off newCmd would trip this test at the flag lookup.
func TestNewFlagBaseRegistered(t *testing.T) {
	f := newCmd.Flags().Lookup("base")
	if f == nil {
		t.Fatal("--base flag not registered on `wrk new`")
	}
	if f.DefValue != "" {
		t.Errorf("--base default = %q, want empty (empty means: use current HEAD/@)",
			f.DefValue)
	}
}
