package engine

import (
	"bytes"
	"os"
	"os/exec"
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
	err := NewWorkspace(repo, "feature", "", Options{DryRun: true,
		Stdout: &out})
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
	err := NewWorkspace(repo, ".", "", Options{DryRun: true,
		Stdout: &out})
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

// TestNewWorkspaceSkipsLinkOnCleanPrimary: an empty plan skips Link on
// the primary. Signal: staged detach entry survives.
func TestNewWorkspaceSkipsLinkOnCleanPrimary(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources: []\n",
	})

	if err := recordDetached(repo, []string{"marker"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}

	var out bytes.Buffer
	if err := NewWorkspace(repo, "feature", "", Options{Stdout: &out,
		StorageRoot: storageIn(t, repo.Root)}); err != nil {
		t.Fatalf("NewWorkspace: %v\nstdout:\n%s", err, out.String())
	}

	reg := readRegistry(t, repo)
	entry := reg[repo.Root]
	if len(entry) != 1 || entry[0] != "marker" {
		t.Fatalf(
			"primary detach entry lost: got %v, want [marker] — Link ran on a clean primary.",
			entry,
		)
	}
}

// TestNewWorkspaceRunsLinkWhenPrimaryHasActions: a non-empty plan runs
// Link. Signal: staged detach entry is cleared.
func TestNewWorkspaceRunsLinkWhenPrimaryHasActions(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	writeFile(t, filepath.Join(repo.Root, ".env"), "")

	if err := recordDetached(repo, []string{"marker"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}

	var out bytes.Buffer
	if err := NewWorkspace(repo, "feature", "", Options{Stdout: &out,
		StorageRoot: storageIn(t, repo.Root)}); err != nil {
		t.Fatalf("NewWorkspace: %v\nstdout:\n%s", err, out.String())
	}

	if _, ok := readRegistry(t, repo)[repo.Root]; ok {
		t.Fatal("primary detach entry survived a non-empty-plan Link — clearDetached didn't run.")
	}
}

// TestNewWorkspaceValidatesDestinationBeforePrimary: an invalid
// destination MUST short-circuit before the primary Link. Signals:
// detach entry survives AND .env is not symlinked.
func TestNewWorkspaceValidatesDestinationBeforePrimary(t *testing.T) {
	repo := newTestRepo(t)

	if err := os.WriteFile(
		filepath.Join(repo.Root, ".wrk.yml"),
		[]byte("resources:\n  - name: env\n    path: .env\n"),
		0o644,
	); err != nil {
		t.Fatalf("write .wrk.yml: %v", err)
	}
	writeFile(t, filepath.Join(repo.Root, ".env"), "")

	if err := recordDetached(repo, []string{"marker"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}

	var out bytes.Buffer
	err := NewWorkspace(repo, ".", "", Options{Stdout: &out,
		StorageRoot: storageIn(t, repo.Root)})
	if err == nil {
		t.Fatalf("NewWorkspace(.) succeeded; want validation error\nstdout:\n%s", out.String())
	}

	entry := readRegistry(t, repo)[repo.Root]
	if len(entry) != 1 || entry[0] != "marker" {
		t.Fatalf("primary detach entry lost: got %v — primary Link ran before dest check.", entry)
	}

	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal(".env was symlinked as a side effect of failed `wrk new .`.")
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

	if err := NewWorkspace(primary, "feature", "", Options{StorageRoot: storage,
		Stdout: &bytes.Buffer{}}); err != nil {
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
	err := NewWorkspace(primary, "./inside", "", Options{StorageRoot: storageIn(t, primary.Root),
		Stdout: &out})
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

// TestNewWorkspaceWithBaseThreadsToBackend pins that a non-empty base
// reaches the git backend: the new worktree ends up on a branch
// whose name matches the destination basename — the shape only the
// `--base` code path in gitBackend.createWorkspace produces. If the
// engine dropped `base` on the floor (or swapped it with
// `destination`), the new worktree would be on the primary's HEAD
// branch and this test would fail.
func TestNewWorkspaceWithBaseThreadsToBackend(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources: []\n",
	})

	// Pre-existing branch to fork off of.
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = primary.Root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("branch", "feature-base")

	var out bytes.Buffer
	if err := NewWorkspace(primary, "secondary", "feature-base", Options{
		StorageRoot: storageIn(t, primary.Root),
		Stdout:      &out,
	}); err != nil {
		t.Fatalf("NewWorkspace with base: %v\nstdout:\n%s", err, out.String())
	}

	parent := filepath.Dir(primary.Root)
	newWs := canonPath(t, filepath.Join(parent, "secondary"))
	if _, err := os.Stat(newWs); err != nil {
		t.Fatalf("stat new workspace: %v", err)
	}

	// The branch the backend created off feature-base is named
	// after the destination basename — the observable signal that
	// base reached `git worktree add -b`.
	head := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	head.Dir = newWs
	got, err := head.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v\n%s", err, got)
	}
	if got := string(bytes.TrimSpace(got)); got != "secondary" {
		t.Errorf("HEAD branch = %q, want %q", got, "secondary")
	}
}

// TestNewWorkspaceDryRunAnnouncesBase pins the dry-run preview's
// user-facing signal for --base: when base is non-empty, the "Would
// create workspace at ..." line MUST name the base ref so the user
// can verify the fork target before committing to it.
func TestNewWorkspaceDryRunAnnouncesBase(t *testing.T) {
	repo := newTestRepo(t)

	if err := os.WriteFile(
		filepath.Join(repo.Root, ".wrk.yml"),
		[]byte("resources: []\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := NewWorkspace(repo, "feature", "some-ref", Options{
		DryRun: true,
		Stdout: &out,
	}); err != nil {
		t.Fatalf("NewWorkspace(dry-run with base): %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("(based on some-ref)")) {
		t.Errorf("dry-run output missing base annotation:\n%s", out.String())
	}
}
