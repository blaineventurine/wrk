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

// TestNewWorkspaceCreatesAndLinks pins the whole point of `wrk new`:
// from the primary, it creates a real sibling worktree (via
// `git worktree add`) and then runs Link inside it so the new workspace
// is wired up immediately. The wiring is observable as a symlink at
// the resource path in the new workspace, pointing at the shared copy
// the primary just provisioned.
//
// Requires a committed .wrk.yml so the new worktree — a checkout of
// the branch — actually contains a config for the second Link to
// operate on.
func TestNewWorkspaceCreatesAndLinks(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, primary.Root)

	// Untracked .env in the primary — the first Link inside
	// NewWorkspace moves it to shared storage and symlinks it back.
	writeFile(t, filepath.Join(primary.Root, ".env"), "provisioned\n")

	if err := NewWorkspace(primary, "feature", Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	// The new worktree exists as a sibling of the primary.
	parent := filepath.Dir(primary.Root)
	newWs := canonPath(t, filepath.Join(parent, "feature"))

	// And its .env is a symlink to the shared copy provisioned by
	// the primary's earlier Link.
	info, err := os.Lstat(filepath.Join(newWs, ".env"))
	if err != nil {
		t.Fatalf("lstat new .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("new workspace .env not a symlink; mode=%v", info.Mode())
	}
	link, err := os.Readlink(filepath.Join(newWs, ".env"))
	if err != nil {
		t.Fatalf("readlink new .env: %v", err)
	}
	// Shared path uses primary.RepositoryID (same repo, same ID).
	wantShared, err := filepath.Abs(filepath.Join(storage, primary.RepositoryID, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if link != wantShared {
		t.Errorf("new .env symlink = %q, want %q", link, wantShared)
	}

	// And the content read through the new workspace matches the
	// original bytes.
	got, err := os.ReadFile(filepath.Join(newWs, ".env"))
	if err != nil {
		t.Fatalf("read via symlink: %v", err)
	}
	if string(got) != "provisioned\n" {
		t.Errorf("new workspace content = %q, want %q", got, "provisioned\n")
	}
}

// TestNewWorkspaceFailsOnNestedDestination pins the nesting guard:
// `wrk new ./inside` from the primary resolves to a path under the
// primary's root, and ResolveDestination MUST refuse it with a message
// that names the offending workspace so the user can fix the command.
//
// The path has a separator (starts with "./") so it is treated as an
// explicit relative path — exactly the trap the guard exists to catch.
func TestNewWorkspaceFailsOnNestedDestination(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources: []\n",
	})

	var out bytes.Buffer
	err := NewWorkspace(primary, "./inside", Options{
		StorageRoot: storageIn(t, primary.Root),
		Stdout:      &out,
	})
	if err == nil {
		t.Fatalf("NewWorkspace(./inside) succeeded; want nesting error\nstdout:\n%s", out.String())
	}
	if !contains(err.Error(), "inside existing workspace") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "inside existing workspace")
	}

	// No worktree should have been created.
	if _, statErr := os.Stat(filepath.Join(primary.Root, "inside")); statErr == nil {
		t.Errorf("nested destination was created despite the guard")
	}
}

// contains is a tiny substring check used only by the new tests above.
// Keeping it inline avoids reaching for `strings.Contains` in a file
// whose existing imports are already lean.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
