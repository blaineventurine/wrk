package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestResolveDestinationBareNameBecomesSibling(t *testing.T) {
	got := resolveDestination("/proj/main", "feature")
	want := filepath.Clean("/proj/feature")
	if got != want {
		t.Fatalf("resolveDestination(%q, %q) = %q, want %q",
			"/proj/main", "feature", got, want)
	}
}

func TestResolveDestinationAbsolutePathIsRespected(t *testing.T) {
	got := resolveDestination("/proj/main", "/somewhere/else")
	if got != filepath.Clean("/somewhere/else") {
		t.Fatalf("absolute path should be untouched, got %q", got)
	}
}

func TestResolveDestinationExplicitRelativeStaysRelativeToRoot(t *testing.T) {
	// A path with a separator is treated literally against root so
	// long-time users of `wrk new ../feature` keep the same behaviour.
	cases := map[string]string{
		"../feature":  "/proj/feature",
		"./sub/thing": "/proj/main/sub/thing",
		"sub/thing":   "/proj/main/sub/thing",
	}
	for input, want := range cases {
		got := resolveDestination("/proj/main", input)
		if got != filepath.Clean(want) {
			t.Errorf("resolveDestination(%q, %q) = %q, want %q",
				"/proj/main", input, got, filepath.Clean(want))
		}
	}
}

func TestResolveDestinationDotAndDotDotAreLiteral(t *testing.T) {
	// "." and ".." are not bare names — they mean the current or
	// parent directory literally, and should not become "../." or
	// "../..".
	if got := resolveDestination("/proj/main", "."); got != filepath.Clean("/proj/main") {
		t.Errorf(`resolveDestination(root, ".") = %q, want root itself`, got)
	}
	if got := resolveDestination("/proj/main", ".."); got != filepath.Clean("/proj") {
		t.Errorf(`resolveDestination(root, "..") = %q, want parent`, got)
	}
}

func TestContainingWorkspaceDetectsEqualAndNested(t *testing.T) {
	workspaces := []string{"/proj/main", "/proj/feature"}

	cases := []struct {
		name string
		dest string
		want string
	}{
		{"equal to workspace", "/proj/main", "/proj/main"},
		{"nested inside workspace", "/proj/main/nested", "/proj/main"},
		{"deeply nested", "/proj/feature/a/b/c", "/proj/feature"},
		{"sibling is fine", "/proj/other", ""},
		{"parent of workspaces is fine", "/proj", ""},
		{"unrelated tree is fine", "/elsewhere/foo", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := containingWorkspace(tc.dest, workspaces)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("containingWorkspace(%q) = %q, want %q",
					tc.dest, got, tc.want)
			}
		})
	}
}

func TestContainingWorkspacePrefixMatchIsNotSubdirectory(t *testing.T) {
	// /proj/main-2 is NOT inside /proj/main, even though the string
	// starts with the same prefix. The path-component check must
	// catch this — a naive strings.HasPrefix would falsely match.
	workspaces := []string{"/proj/main"}
	got, err := containingWorkspace("/proj/main-2", workspaces)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("prefix-only match should not be inside; got %q", got)
	}
}

// TestResolveDestinationRejectsEmpty pins S8: an empty destination
// used to fall through resolveDestination, collapse to r.Root, and
// then hit the "destination already exists" branch — a confusing
// error for what is really a "you didn't name anything" mistake. The
// upfront check now rejects it cleanly with a message that quotes the
// bad input.
func TestResolveDestinationRejectsEmpty(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination("")
	if err == nil {
		t.Fatal("ResolveDestination(\"\") should error")
	}
	if !strings.Contains(err.Error(), "destination cannot be") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveDestinationRejectsDot pins S8: "." is not a workspace
// destination — it means "the current directory", which is the
// existing workspace. Reject it up front instead of letting the
// "already exists" check catch it downstream.
func TestResolveDestinationRejectsDot(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination(".")
	if err == nil {
		t.Fatal(`ResolveDestination(".") should error`)
	}
	if !strings.Contains(err.Error(), `"."`) {
		t.Fatalf("error should quote the bad destination, got: %v", err)
	}
}

// TestResolveDestinationRejectsDotDot pins S8: ".." would land on the
// parent directory, which is almost certainly not what the user
// wanted. Reject with the same message shape as "".
func TestResolveDestinationRejectsDotDot(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination("..")
	if err == nil {
		t.Fatal(`ResolveDestination("..") should error`)
	}
	if !strings.Contains(err.Error(), `".."`) {
		t.Fatalf("error should quote the bad destination, got: %v", err)
	}
}

// TestResolveDestinationRejectsWhitespace pins S8: `wrk new "  "`
// should fail cleanly. strings.TrimSpace makes whitespace-only
// destinations equivalent to the empty case, so the user does not
// wander into a "path exists at /proj/main/  " error.
func TestResolveDestinationRejectsWhitespace(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination("  ")
	if err == nil {
		t.Fatal(`ResolveDestination("  ") should error`)
	}
	if !strings.Contains(err.Error(), "destination cannot be") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateWorkspaceFullFlow exercises the whole
// Detect → CreateWorkspace → Detect(destination) chain against a real
// git repo. It pins:
//   - the bare name "feature" is resolved to a sibling of the primary
//   - `git worktree add` runs successfully
//   - the returned *Repository is rooted at the NEW worktree, not the
//     primary — a caller could chain wrk commands against the new
//     workspace without a second Detect
func TestCreateWorkspaceFullFlow(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect(primary): %v", err)
	}

	newRepo, err := repo.CreateWorkspace("feature", "", nil)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	wantRoot := filepath.Join(parent, "feature")
	if newRepo.Root != wantRoot {
		t.Fatalf("new Repository.Root = %q, want %q (sibling of primary)",
			newRepo.Root, wantRoot)
	}

	// The worktree really exists on disk with the linked-worktree
	// gitdir file — this is what makes Detect(newRepo.Root) work.
	info, err := os.Stat(wantRoot)
	if err != nil {
		t.Fatalf("stat new workspace: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("new workspace %q is not a directory", wantRoot)
	}

	// Same repository ID as the primary — every workspace of the
	// same repo MUST hash to the same identity, otherwise the
	// detach registry and shared storage would fork.
	if newRepo.RepositoryID != repo.RepositoryID {
		t.Fatalf("new workspace repository ID = %q, want %q (same repo)",
			newRepo.RepositoryID, repo.RepositoryID)
	}
}

func TestCreateJJWorkspacePreservesRemoteRepositoryID(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	makeDir(t, primary)
	initColocatedJJRepo(t, primary)

	cmd := exec.Command(
		"git", "-C", primary, "remote", "add", "origin",
		"git@github.com:org/repo.git",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect(primary): %v", err)
	}

	newRepo, err := repo.CreateWorkspace("feature", "", nil)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if newRepo.RepositoryID != repo.RepositoryID {
		t.Fatalf("new jj workspace repository ID = %q, want %q (same repo)",
			newRepo.RepositoryID, repo.RepositoryID)
	}
}

// TestCreateWorkspaceRefusesNesting pins the ResolveDestination guard:
// asking to create a workspace INSIDE an existing one MUST fail with
// a message users can act on. Nested worktrees confuse git and jj,
// and wrk's shared-storage links assume siblings.
func TestCreateWorkspaceRefusesNesting(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// "./inside" is an explicit relative path so it stays under the
	// primary — resolveDestination treats it literally, and then the
	// containingWorkspace check MUST fire.
	_, err = repo.CreateWorkspace("./inside", "", nil)
	if err == nil {
		t.Fatal("CreateWorkspace(./inside): expected nesting error")
	}
	if !strings.Contains(err.Error(), "inside existing workspace") {
		t.Fatalf("error missing nesting phrase; got: %v", err)
	}

	// The nested directory MUST NOT have been created — the guard
	// runs BEFORE the backend touches disk.
	if _, statErr := os.Stat(filepath.Join(primary, "inside")); !os.IsNotExist(statErr) {
		t.Fatalf("nested destination should not exist; stat err = %v", statErr)
	}
}

// TestCreateWorkspaceRefusesExistingDestination pins the "already
// exists" preflight: attempting to create a workspace at a path that
// already exists MUST fail cleanly, not clobber the directory and
// not partially initialize a worktree there.
func TestCreateWorkspaceRefusesExistingDestination(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	// Pre-create the sibling destination "feature" with a marker
	// file inside so we can prove it was untouched afterwards.
	dest := filepath.Join(parent, "feature")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "marker.txt")
	if err := os.WriteFile(marker, []byte("pre-existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	_, err = repo.CreateWorkspace("feature", "", nil)
	if err == nil {
		t.Fatal("CreateWorkspace(feature): expected 'already exists' error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error missing 'already exists'; got: %v", err)
	}

	// Marker file MUST still be there — the preflight ran BEFORE
	// git got involved.
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker file gone: %v", err)
	}
	if string(data) != "pre-existing\n" {
		t.Fatalf("marker overwritten: got %q", string(data))
	}
}

// TestRepositoryWorkspacesReturnsAllLive covers the exported
// Workspaces method: after `wrk new feature`, listing from the
// PRIMARY MUST include both workspaces. This is what powers
// `wrk list` — a regression that silently drops the primary or
// misses secondaries would break the whole overview.
func TestRepositoryWorkspacesReturnsAllLive(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if _, err := repo.CreateWorkspace("feature", "", nil); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	got, err := repo.Workspaces()
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}

	sort.Strings(got)
	want := []string{
		filepath.Join(parent, "feature"),
		primary,
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("Workspaces() = %v (%d), want %v (%d)",
			got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Workspaces()[%d] = %q, want %q",
				i, got[i], want[i])
		}
	}
}

// TestCreateWorkspaceBackendFailurePropagates pins that when the
// backend's createWorkspace fails, CreateWorkspace does NOT swallow
// the error and does NOT return a bogus *Repository. A caller that
// switched on (nil, nil) vs (nil, err) needs the (nil, err) contract
// honored end to end.
//
// We provoke the failure with a branch conflict: git derives the
// worktree branch name from the last path component, and git refuses
// to check out a branch that is already used by another worktree.
// ResolveDestination sees no directory at `../existing` so control
// reaches the backend call, which then fails.
func TestCreateWorkspaceBackendFailurePropagates(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	// Create branch "existing" and check it out at ../feature.
	// The branch is now claimed by that worktree and any subsequent
	// `git worktree add ../existing` (which derives the branch name
	// from the last component) will fail.
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = primary
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("branch", "existing")
	runGit("worktree", "add", "--quiet",
		filepath.Join(parent, "feature"), "existing")

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	newRepo, err := repo.CreateWorkspace("existing", "", nil)
	if err == nil {
		t.Fatalf("CreateWorkspace: got %v, want error from branch conflict",
			newRepo)
	}
	if newRepo != nil {
		t.Fatalf("CreateWorkspace error path returned %v, want nil", newRepo)
	}
	// Wrapped by passthrough — MUST name git.
	if !strings.Contains(err.Error(), "git") {
		t.Fatalf("error missing command name: %v", err)
	}
}

// TestResolveDestinationPropagatesWorkspacesError pins that when the
// backend's Workspaces() call fails mid-preflight, ResolveDestination
// returns that error — it does NOT swallow it and let a stale
// workspace list drive the containingWorkspace decision. A caller
// that silently proceeded on a broken repo could create a "sibling"
// workspace directly inside a workspace it no longer sees.
//
// We provoke the failure by Detecting a valid git repo, then rm -rf
// .git out-of-band — subsequent `git worktree list` fails, and the
// error MUST surface all the way up through ResolveDestination.
func TestResolveDestinationPropagatesWorkspacesError(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// Break the repo AFTER detection so ResolveDestination's own
	// Workspaces call is what fails, not Detect.
	if err := os.RemoveAll(filepath.Join(primary, ".git")); err != nil {
		t.Fatalf("rm .git: %v", err)
	}

	dest, err := repo.ResolveDestination("feature")
	if err == nil {
		t.Fatalf("ResolveDestination on broken repo: got %q, want error",
			dest)
	}
	if dest != "" {
		t.Fatalf("ResolveDestination error path returned %q, want empty",
			dest)
	}
}

// TestCreateWorkspaceWithBaseThreadsToBackend pins that the Repository
// wrapper forwards a non-empty base to the backend, which then forks
// the new worktree off that ref. The check is indirect but strong:
// after CreateWorkspace(dest, "feature-base"), the new worktree lives
// on a branch named after the destination basename — the shape only
// the `--base` code path in gitBackend.createWorkspace produces.
// A regression that dropped base (`r.backend.createWorkspace(root,
// dest, "")`) would land on the primary's HEAD branch instead.
func TestCreateWorkspaceWithBaseThreadsToBackend(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	primary := filepath.Join(parent, "main")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, primary)

	// Pre-existing branch to fork off of.
	branchCmd := exec.Command("git", "branch", "feature-base")
	branchCmd.Dir = primary
	if out, err := branchCmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch feature-base: %v\n%s", err, out)
	}

	repo, err := Detect(primary, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	newRepo, err := repo.CreateWorkspace("secondary", "feature-base", nil)
	if err != nil {
		t.Fatalf("CreateWorkspace with base: %v", err)
	}

	// The wrapper's own contract: newRepo is rooted at the new
	// worktree, not the primary.
	wantRoot := filepath.Join(parent, "secondary")
	if newRepo.Root != wantRoot {
		t.Fatalf("new Repository.Root = %q, want %q",
			newRepo.Root, wantRoot)
	}

	// And the backend's --base branch semantics reached disk: the
	// new worktree's HEAD sits on a branch named after the
	// destination basename.
	got, err := capture(wantRoot, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if strings.TrimSpace(got) != "secondary" {
		t.Errorf("HEAD branch = %q, want %q", strings.TrimSpace(got), "secondary")
	}
}
