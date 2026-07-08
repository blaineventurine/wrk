package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestJJCommonDirRequiresColocation pins S10: any failure from
// `jj git root` is wrapped with wrk's colocation requirement so users
// understand why detection failed instead of chasing jj's internal
// "not a colocated repo" error.
func TestJJCommonDirRequiresColocation(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}

	// Empty temp dir — no `.jj`, no `.git`. `jj git root` will fail;
	// the wrap must fire regardless of the underlying jj message so
	// users see the requirement even when jj's phrasing changes
	// between releases.
	_, err := jjBackend{}.commonDir(t.TempDir())
	if err == nil {
		t.Fatal("jjBackend.commonDir: expected error in non-repo directory")
	}
	if !strings.Contains(err.Error(), "colocated") {
		t.Fatalf("error missing colocation guidance: %v", err)
	}
}

// TestJJBackendKind pins the trivial dispatcher.
func TestJJBackendKind(t *testing.T) {
	if got := (jjBackend{}).kind(); got != JJ {
		t.Fatalf("jjBackend.kind() = %q, want %q", got, JJ)
	}
}

// TestJJBackendCreateWorkspace exercises the real `jj workspace add`
// path in a colocated repo. Success means the destination lives on
// disk AND jj's own workspace list registers it — anything else means
// the workspace was orphaned by an incomplete add.
func TestJJBackendCreateWorkspace(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	dest := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, dest); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("destination %q is not a directory", dest)
	}

	// Registered? `jj workspace list` (from the primary) must show
	// both roots — a passthrough failure or wrong `--` handling
	// would create the dir but leave jj with no record of it.
	got, err := (jjBackend{}).workspaces(root)
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}
	sort.Strings(got)
	want := []string{dest, root}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaces = %v, want %v", got, want)
	}
}

// TestJJBackendWorkspacesListsAll asserts that workspaces() returns
// EVERY registered workspace via the `self.root()` template — the
// template is our contract for a version-agnostic canonical path
// column, and a regression to the old free-text output would parse
// half the words as paths.
func TestJJBackendWorkspacesListsAll(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, secondary); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}

	got, err := (jjBackend{}).workspaces(root)
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	// jj's listing order is not part of our contract, so sort.
	sort.Strings(got)
	want := []string{root, secondary}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaces(%q) = %v, want %v",
			root, got, want)
	}
}

// TestJJBackendCommonDirColocatedReturnsGitDir pins that commonDir on
// a colocated repo returns the .git directory — this is what wrk uses
// for identity hashing and the detach registry, both of which live
// under the shared git metadata.
func TestJJBackendCommonDirColocatedReturnsGitDir(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	initColocatedJJRepo(t, root)

	got, err := (jjBackend{}).commonDir(root)
	if err != nil {
		t.Fatalf("commonDir: %v", err)
	}

	want := filepath.Join(root, ".git")
	if canonPath(t, got) != canonPath(t, want) {
		t.Fatalf("commonDir = %q, want %q (colocated .git)",
			got, want)
	}
}

// TestJJBackendCommonDirWrapsBrokenRepo pins that any failure of
// `jj git root` is wrapped with wrk's colocation guidance — the
// concrete requirement wrk enforces on top of jj's own errors.
//
// We provoke the failure by initializing a colocated repo and then
// removing its .git directory, which is the shape a user hits when
// git-side state is nuked out-of-band. The wrap MUST still mention
// "colocated" so the user has a hint about wrk's requirement.
//
// Note (2026-07): on jj >= 0.43 `jj git root` in a --no-colocate repo
// no longer fails; it returns the internal `.jj/repo/store/git`
// path. That means the "pure jj, no colocate" scenario is no longer
// detectable through this branch of the SUT — the wrap fires only on
// genuine jj-side failures, not on structurally non-colocated
// repos. This test therefore covers the still-live failure path; the
// pure non-colocated case is intentionally not tested because the
// SUT cannot distinguish it on current jj.
func TestJJBackendCommonDirWrapsBrokenRepo(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	initColocatedJJRepo(t, root)

	// Nuke .git — jj still sees .jj so it tries to talk to git, and
	// git fails. This is the "broken colocation" failure surface.
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("rm .git: %v", err)
	}

	_, err := (jjBackend{}).commonDir(root)
	if err == nil {
		t.Fatal("commonDir on broken repo: expected error")
	}
	if !strings.Contains(err.Error(), "colocated") {
		t.Fatalf("wrap missing colocation guidance: %v", err)
	}
}

// TestJJBackendWorkspacesErrorsOutsideRepo pins the failure path of
// `jj workspace list`: pointing at a directory that isn't a jj repo
// MUST surface an error, not silently return nil. Otherwise a
// caller would treat it as an empty repo and act on nothing.
func TestJJBackendWorkspacesErrorsOutsideRepo(t *testing.T) {
	skipIfNoJJ(t)
	isolateJJConfig(t)

	got, err := (jjBackend{}).workspaces(t.TempDir())
	if err == nil {
		t.Fatalf("workspaces on non-repo: got %v, want error", got)
	}
	if got != nil {
		t.Fatalf("workspaces error path returned %v, want nil slice", got)
	}
}

// TestJJBackendCreateWorkspaceErrorsOutsideRepo pins the passthrough
// failure: `jj workspace add` refuses outside a jj repo. The error
// stops CreateWorkspace from proceeding to Detect on a directory the
// backend never actually populated.
func TestJJBackendCreateWorkspaceErrorsOutsideRepo(t *testing.T) {
	skipIfNoJJ(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	dest := filepath.Join(filepath.Dir(root), "would-be-workspace")

	err := (jjBackend{}).createWorkspace(root, dest)
	if err == nil {
		t.Fatal("createWorkspace outside jj repo: expected error")
	}
	if !strings.Contains(err.Error(), "jj") {
		t.Fatalf("error missing command name: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest should not exist after failed createWorkspace; stat err = %v",
			statErr)
	}
}
