package engine

import (
	"bytes"
	"testing"

	"github.com/blaineventurine/wrk/internal/repository"
)

// TestWorkspaceSummariesRollup pins the per-workspace rollup across
// two live worktrees. One workspace is linked; the other is detached.
// The summaries MUST reflect each workspace's state independently —
// the primary's Detach must not leak into the secondary's WorkspaceState.
func TestWorkspaceSummariesRollup(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, primary.Root)

	writeFile(t, primary.Root+"/.env", "primary\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(primary, opts); err != nil {
		t.Fatalf("Link primary: %v", err)
	}

	_, secondary := addGitWorktree(t, primary, "feature")
	if err := Link(secondary, opts); err != nil {
		t.Fatalf("Link secondary: %v", err)
	}

	// Diverge: detach the primary; the secondary stays linked.
	if err := Detach(primary, opts); err != nil {
		t.Fatalf("Detach primary: %v", err)
	}

	summaries, err := WorkspaceSummaries(primary, opts)
	if err != nil {
		t.Fatalf("WorkspaceSummaries: %v", err)
	}

	byRoot := map[string]WorkspaceSummary{}
	for _, s := range summaries {
		byRoot[s.Root] = s
	}
	if got := len(byRoot); got != 2 {
		t.Fatalf("summaries spanned %d workspaces, want 2 (got=%+v)", got, summaries)
	}

	if s, ok := byRoot[primary.Root]; !ok {
		t.Fatalf("primary summary missing: %+v", byRoot)
	} else if s.State != WorkspaceDetached {
		t.Errorf("primary State = %q, want %q", s.State, WorkspaceDetached)
	}

	if s, ok := byRoot[secondary.Root]; !ok {
		t.Fatalf("secondary summary missing: %+v", byRoot)
	} else if s.State != WorkspaceLinked {
		t.Errorf("secondary State = %q, want %q", s.State, WorkspaceLinked)
	}
}

// TestWorkspaceSummariesMarksCurrent is the regression for B1/B4:
// canonicalization means the workspace matching cwd is marked
// IsCurrent, even on macOS where /var/folders is a symlink to
// /private/var/folders. On the primary's summary run, only the
// primary is current; on the secondary's, only the secondary.
func TestWorkspaceSummariesMarksCurrent(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n    create: false\n",
	})
	storage := storageIn(t, primary.Root)
	// A second worktree so the "which one is current?" question has a
	// distinguishable answer.
	_, secondary := addGitWorktree(t, primary, "feature")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	// Query from the primary's Repository → only the primary is current.
	primarySummaries, err := WorkspaceSummaries(primary, opts)
	if err != nil {
		t.Fatalf("WorkspaceSummaries(primary): %v", err)
	}
	assertCurrent(t, primarySummaries, primary.Root, "primary vantage")

	// Query from the secondary's Repository → only the secondary is current.
	secondarySummaries, err := WorkspaceSummaries(secondary, opts)
	if err != nil {
		t.Fatalf("WorkspaceSummaries(secondary): %v", err)
	}
	assertCurrent(t, secondarySummaries, secondary.Root, "secondary vantage")
}

// assertCurrent verifies exactly one summary is marked IsCurrent, and
// that its Root matches expectedRoot. Any other configuration is a
// regression in the canonicalization check.
func assertCurrent(t *testing.T, summaries []WorkspaceSummary, expectedRoot, tag string) {
	t.Helper()
	var currentCount int
	var currentRoot string
	for _, s := range summaries {
		if s.IsCurrent {
			currentCount++
			currentRoot = s.Root
		}
	}
	if currentCount != 1 {
		t.Errorf("[%s] IsCurrent count = %d, want 1 (summaries=%+v)", tag, currentCount, summaries)
	}
	if currentRoot != expectedRoot {
		t.Errorf("[%s] IsCurrent root = %q, want %q", tag, currentRoot, expectedRoot)
	}
}

// ensure repository stays imported for the linter.
var _ = repository.Auto
