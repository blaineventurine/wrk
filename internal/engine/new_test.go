package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestNewWorkspaceDryRunHasNoSideEffects pins the `wrk new --dry-run`
// contract: with Options.DryRun set, NewWorkspace does not invoke the
// backend's createWorkspace at all — no new directories, no
// `git worktree add`. Verified by snapshotting the parent directory
// before and after, and by confirming the destination path is absent
// on disk after the call returns.
func TestNewWorkspaceDryRunHasNoSideEffects(t *testing.T) {
	repo := newTestRepo(t)

	// A minimal wrk config so BuildLinkPlan produces a valid (empty)
	// plan. Without this, Link fails on missing .wrk.yml and we never
	// reach the dry-run branch we want to pin.
	if err := os.WriteFile(
		filepath.Join(repo.Root, ".wrk.yml"),
		[]byte("resources: []\n"),
		0o644,
	); err != nil {
		t.Fatalf("write .wrk.yml: %v", err)
	}

	parent := filepath.Dir(repo.Root)
	before := listNames(t, parent)

	var out bytes.Buffer
	err := NewWorkspace(repo, "feature", Options{
		DryRun: true,
		Stdout: &out,
	})
	if err != nil {
		t.Fatalf("NewWorkspace(dry-run): %v", err)
	}

	// The dry-run branch prints the resolved destination — that's the
	// user-facing signal that the preflight ran.
	expected := filepath.Join(parent, "feature")
	if !bytes.Contains(out.Bytes(), []byte(expected)) {
		t.Fatalf(
			"dry-run output missing resolved destination %q\nstdout:\n%s",
			expected, out.String(),
		)
	}
	if !bytes.Contains(out.Bytes(), []byte("Would create workspace")) {
		t.Fatalf(
			"dry-run output missing preview banner\nstdout:\n%s",
			out.String(),
		)
	}

	// No new directory should have appeared next to the primary
	// workspace — that's the whole point of --dry-run.
	after := listNames(t, parent)
	if !equalSlice(before, after) {
		t.Fatalf(
			"dry-run mutated parent dir\n before: %v\n after: %v",
			before, after,
		)
	}
	if _, err := os.Stat(expected); err == nil {
		t.Fatalf(
			"dry-run created destination %q; expected no side effect",
			expected,
		)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat(destination): %v", err)
	}
}

// TestNewWorkspaceDryRunSurfacesResolutionErrors confirms that
// destination validation still runs under --dry-run: nesting inside
// an existing workspace is caught up-front, so the preview mode is
// not a false-positive machine.
func TestNewWorkspaceDryRunSurfacesResolutionErrors(t *testing.T) {
	repo := newTestRepo(t)

	if err := os.WriteFile(
		filepath.Join(repo.Root, ".wrk.yml"),
		[]byte("resources: []\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// "." resolves inside the current workspace root — CreateWorkspace
	// would reject this, and ResolveDestination must reject it too so
	// the preview stays honest.
	var out bytes.Buffer
	err := NewWorkspace(repo, ".", Options{
		DryRun: true,
		Stdout: &out,
	})
	if err == nil {
		t.Fatalf(
			"expected an error for destination inside current workspace, got nil\nstdout:\n%s",
			out.String(),
		)
	}
}

// listNames returns the directory entries at dir, sorted, for stable
// before/after comparison.
func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	sort.Strings(names)
	return names
}

// TestNewWorkspaceSkipsLinkOnCleanPrimary pins D4: when the primary
// workspace has nothing to link (empty config → empty plan), `wrk new`
// MUST NOT trigger Link — no plan output, no clearDetached side effect
// on the primary's registry entry.
//
// Observable signal: clearDetached. Link's success path clears the
// primary's detach-registry entry; skipping Link leaves it alone. We
// stage an entry beforehand, force NewWorkspace to abort AFTER the
// primary-preflight (by creating the destination so ResolveDestination
// refuses it), and check the entry survives.
func TestNewWorkspaceSkipsLinkOnCleanPrimary(t *testing.T) {
	repo := newTestRepo(t)

	// Empty config → empty plan → the D4 skip should fire.
	if err := os.WriteFile(
		filepath.Join(repo.Root, ".wrk.yml"),
		[]byte("resources: []\n"),
		0o644,
	); err != nil {
		t.Fatalf("write .wrk.yml: %v", err)
	}

	// Stage a detach-registry entry for the primary. If Link runs, its
	// clearDetached step will remove this — that's the discriminator.
	if err := recordDetached(repo, []string{"marker"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}

	// Force the destination check to fail so NewWorkspace returns before
	// touching the new workspace at all. Whatever happened to the primary
	// (Link run vs skipped) is by then already committed.
	parent := filepath.Dir(repo.Root)
	dest := filepath.Join(parent, "feature")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}

	var out bytes.Buffer
	err := NewWorkspace(repo, "feature", Options{Stdout: &out})
	if err == nil {
		t.Fatalf(
			"expected error (destination exists), got nil\nstdout:\n%s",
			out.String(),
		)
	}

	// The primary's detach entry MUST still be there: skipping Link
	// means clearDetached was never called.
	reg := readRegistry(t, repo)
	entry := reg[repo.Root]
	if len(entry) != 1 || entry[0] != "marker" {
		t.Fatalf(
			"primary detach entry lost: got %v, want [marker]\n"+
				"Link ran on a clean primary — D4 regression.",
			entry,
		)
	}
}

// TestNewWorkspaceRunsLinkWhenPrimaryHasActions confirms the flip side
// of D4: a primary with a non-empty plan still runs Link. Signal: the
// staged detach entry IS cleared, because Link's success path clears it.
func TestNewWorkspaceRunsLinkWhenPrimaryHasActions(t *testing.T) {
	repo := newTestRepo(t)

	// One resource that isn't yet linked → BuildLinkPlan produces at
	// least one action (Move / CreateSymlink), which trips the D4 gate
	// and forces Link to run.
	if err := os.WriteFile(
		filepath.Join(repo.Root, ".wrk.yml"),
		[]byte("resources:\n  - name: env\n    path: .env\n"),
		0o644,
	); err != nil {
		t.Fatalf("write .wrk.yml: %v", err)
	}
	// Ensure the resource exists so a Move action is planned rather than
	// a NotFound-style skip; empty file is enough.
	if err := os.WriteFile(
		filepath.Join(repo.Root, ".env"),
		nil,
		0o644,
	); err != nil {
		t.Fatalf("touch .env: %v", err)
	}

	if err := recordDetached(repo, []string{"marker"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}

	// Same abort-before-CreateWorkspace trick so we don't need a real
	// git-worktree-add setup.
	parent := filepath.Dir(repo.Root)
	dest := filepath.Join(parent, "feature")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}

	var out bytes.Buffer
	err := NewWorkspace(repo, "feature", Options{
		Stdout:      &out,
		StorageRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatalf(
			"expected error (destination exists), got nil\nstdout:\n%s",
			out.String(),
		)
	}

	reg := readRegistry(t, repo)
	if _, ok := reg[repo.Root]; ok {
		t.Fatalf(
			"primary detach entry survived a non-empty-plan Link: %v\n"+
				"Link should have run (plan had actions) and cleared it.",
			reg[repo.Root],
		)
	}
}
