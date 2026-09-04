package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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
	if err := (jjBackend{}).createWorkspace(root, dest, "", nil); err != nil {
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

// TestJJBackendCreateWorkspaceWithBase exercises the `--revision`
// path: given a colocated repo, the backend forks the new workspace's
// @ off a chosen change_id rather than the invoking workspace's
// default parent. Success means the destination exists AND its @-
// (parent of the working-copy commit) equals the requested base.
// This would fail if the backend accidentally dropped --revision
// (@- would still equal root but the assertion would collapse to the
// legacy behavior) or fed the base after the `--` separator (jj would
// parse it as a second path argument and error).
func TestJJBackendCreateWorkspaceWithBase(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	// Pin a stable base rev: the root() change_id is the same across
	// every workspace in the repo, so the equality assertion below
	// doesn't drift with whichever change happens to be @.
	baseChange, err := capture(root, "jj", "log", "-r", "@-",
		"--no-graph", "-T", "change_id")
	if err != nil {
		t.Fatalf("read base change_id: %v", err)
	}
	baseChange = strings.TrimSpace(baseChange)
	if baseChange == "" {
		t.Fatal("empty base change_id")
	}

	dest := filepath.Join(parent, "secondary")
	if err := (jjBackend{}).createWorkspace(root, dest, baseChange, nil); err != nil {
		t.Fatalf("createWorkspace with base: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest not created: %v", err)
	}

	// The new workspace's @- MUST equal the base we requested. If
	// the backend forgot --revision, @- would default to the
	// invoking workspace's @- — which happens to also be root here,
	// so this test doubles as a canary against a swap of `base` and
	// destination positional order.
	got, err := capture(dest, "jj", "log", "-r", "@-",
		"--no-graph", "-T", "change_id")
	if err != nil {
		t.Fatalf("read new @- change_id: %v", err)
	}
	if strings.TrimSpace(got) != baseChange {
		t.Errorf("new workspace @- = %q, want %q", strings.TrimSpace(got), baseChange)
	}
}

// TestJJBackendCreateWorkspaceEmptyBasePreservesLegacyBehavior pins
// the base="" contract: the backend MUST behave exactly like the
// pre-`--base` code path (`jj workspace add -- <dest>`), so callers
// that never set --base see zero behaviour change. Signal: the
// destination is created AND jj's workspace list registers it —
// mirroring the assertions in TestJJBackendCreateWorkspace.
func TestJJBackendCreateWorkspaceEmptyBasePreservesLegacyBehavior(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	dest := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, dest, "", nil); err != nil {
		t.Fatalf("createWorkspace empty base: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest not created: %v", err)
	}

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
	if err := (jjBackend{}).createWorkspace(root, secondary, "", nil); err != nil {
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

func TestResolveJJWorkspacePathsRecoversLegacyCurrentWorkspace(t *testing.T) {
	root := canonPath(t, t.TempDir())
	secondary := canonPath(t, t.TempDir())

	got, err := resolveJJWorkspacePaths(root, []jjWorkspaceEntry{
		{name: "default"},
		{name: "feature", path: secondary},
	})
	if err != nil {
		t.Fatalf("resolveJJWorkspacePaths: %v", err)
	}
	want := []string{root, secondary}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveJJWorkspacePaths = %v, want %v", got, want)
	}
}

func TestResolveJJWorkspacePathsRefusesUnresolvedSibling(t *testing.T) {
	root := canonPath(t, t.TempDir())

	got, err := resolveJJWorkspacePaths(root, []jjWorkspaceEntry{
		{name: "default"},
		{name: "feature", path: root},
	})
	if err == nil {
		t.Fatalf("resolveJJWorkspacePaths = %v, want error", got)
	}
	if !strings.Contains(err.Error(), "default") ||
		!strings.Contains(err.Error(), "recorded root") {
		t.Fatalf("error lacks unresolved-workspace guidance: %v", err)
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

	err := (jjBackend{}).createWorkspace(root, dest, "", nil)
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

// TestJJBackendDetectGhostsFindsRemoved seeds a colocated repo with
// a secondary workspace, deletes its working-copy directory
// out-of-band, and asserts detectGhosts returns exactly the missing
// workspace's path. jj 0.43's `self.root()` template evaluation
// itself emits an inline `<Error: ...>` string for missing
// working copies — the backend recovers the path from that string
// so callers can report and prune the ghost without extra shell-outs.
func TestJJBackendDetectGhostsFindsRemoved(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, secondary, "", nil); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}
	if err := os.RemoveAll(secondary); err != nil {
		t.Fatalf("rm secondary: %v", err)
	}

	got, err := (jjBackend{}).detectGhosts(root)
	if err != nil {
		t.Fatalf("detectGhosts: %v", err)
	}

	want := []string{secondary}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detectGhosts(%q) = %v, want %v", root, got, want)
	}
}

// TestJJBackendDetectGhostsEmptyWhenClean pins the empty case: a
// clean colocated repo returns an empty (non-nil) slice, satisfying
// the backend contract `wrk gc` relies on to distinguish "nothing
// to do" from "backend failed".
func TestJJBackendDetectGhostsEmptyWhenClean(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	initColocatedJJRepo(t, root)

	got, err := (jjBackend{}).detectGhosts(root)
	if err != nil {
		t.Fatalf("detectGhosts: %v", err)
	}

	if got == nil {
		t.Fatalf("detectGhosts(clean) = nil, want []string{}")
	}
	if len(got) != 0 {
		t.Fatalf("detectGhosts(clean) = %v, want empty", got)
	}
}

// TestJJBackendPruneGhostsForgetsAndReports seeds a ghost workspace,
// prunes it, and checks BOTH invariants: the returned slice names
// the pruned path, AND a follow-up workspaces() call no longer lists
// the ghost. jj's `workspace forget` is the only supported way to
// drop the workspace from the repo's metadata; the test would fail
// if the implementation returned the path but never called forget.
func TestJJBackendPruneGhostsForgetsAndReports(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, secondary, "", nil); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}
	if err := os.RemoveAll(secondary); err != nil {
		t.Fatalf("rm secondary: %v", err)
	}

	pruned, err := (jjBackend{}).pruneGhosts(root)
	if err != nil {
		t.Fatalf("pruneGhosts: %v", err)
	}

	want := []string{secondary}
	if !reflect.DeepEqual(pruned, want) {
		t.Fatalf("pruneGhosts(%q) = %v, want %v", root, pruned, want)
	}

	// Post-prune: workspaces() must no longer see the ghost. The
	// primary is still there and it stays the only survivor.
	ws, err := (jjBackend{}).workspaces(root)
	if err != nil {
		t.Fatalf("post-prune workspaces: %v", err)
	}
	if !slices.Equal(ws, []string{root}) {
		t.Fatalf("post-prune workspaces = %v, want [%q]", ws, root)
	}

	// Idempotent: a second prune returns the empty slice, not an
	// error, so callers can call it defensively.
	pruned, err = (jjBackend{}).pruneGhosts(root)
	if err != nil {
		t.Fatalf("second pruneGhosts: %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("second pruneGhosts = %v, want empty", pruned)
	}
}

// TestJJBackendDetectGhostsErrorsOutsideRepo mirrors
// TestJJBackendWorkspacesErrorsOutsideRepo: a directory that isn't
// a jj repo MUST surface the underlying `jj workspace list` error
// rather than a silent empty slice, which callers would treat as
// "clean".
func TestJJBackendDetectGhostsErrorsOutsideRepo(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	got, err := (jjBackend{}).detectGhosts(t.TempDir())
	if err == nil {
		t.Fatalf("detectGhosts outside jj repo: got %v, want error", got)
	}
	if got != nil {
		t.Fatalf("detectGhosts error path returned %v, want nil", got)
	}
}

// TestParseJJInlineErrorExtractsPath is the pure-parser unit test for
// jj 0.43's template-time error string. A regression that dropped the
// LastIndex-based split (say, splitting on the first ": " after the
// name) would truncate the workspace path at the first embedded
// colon and hand callers a bogus prefix to display or prune.
func TestParseJJInlineErrorExtractsPath(t *testing.T) {
	// Exact shape observed on jj 0.43 with a rm -rf'd workspace.
	input := `<Error: Failed to resolve workspace root: feature: /tmp/main/.jj/repo/../../../feature: No such file or directory (os error 2)>`

	got, isErr := parseJJInlineError("feature", input)
	if !isErr {
		t.Fatal("parseJJInlineError: shape was a jj inline error, isErr = false")
	}
	// filepath.Clean collapses `.jj/repo/../../../` down to the
	// actual workspace root, matching what wrk hands to callers.
	want := "/tmp/feature"
	if got != want {
		t.Fatalf("parseJJInlineError = %q, want %q", got, want)
	}
}

// TestParseJJInlineErrorRejectsPlainPath guards the negative branch:
// a plain absolute path (what jj emits for a live workspace) MUST
// come back as (_, false), so callers don't mistake a live entry for
// a ghost and try to forget it.
func TestParseJJInlineErrorRejectsPlainPath(t *testing.T) {
	got, isErr := parseJJInlineError("default", "/tmp/main")
	if isErr {
		t.Fatalf("parseJJInlineError(plain path) = (%q, true), want (_, false)", got)
	}
}

// TestJJBackendRemoveWorkspace pins the happy path: seed a jj
// secondary workspace, forget it via removeWorkspace, and check
// that jj's own workspace list no longer surfaces its name AND
// that the working-copy directory is gone from disk. The backend
// translates the target PATH into the workspace NAME jj requires,
// so a regression that fed the path directly to `workspace forget`
// would produce a jj-side error rather than a silent success.
//
// Directory removal matches the git backend's user-visible contract:
// `jj workspace forget` alone is metadata-only, but `wrk remove`
// promises symmetric behavior across VCS backends. See the fix in
// removeWorkspace: forget then os.RemoveAll.
func TestJJBackendRemoveWorkspace(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	feature := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, feature, "", nil); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}
	// Sanity: directory exists after add so a failed RemoveAll
	// below would not be masked by a pre-fix absence.
	if _, err := os.Stat(feature); err != nil {
		t.Fatalf("secondary workspace missing after createWorkspace: %v", err)
	}

	if err := (jjBackend{}).removeWorkspace(root, feature, false, nil); err != nil {
		t.Fatalf("removeWorkspace: %v", err)
	}

	// Directory MUST be gone: the backend runs os.RemoveAll after
	// `jj workspace forget`. A regression that dropped the RemoveAll
	// would fail this assertion, leaving the exact orphan-dir bug
	// this fix targets.
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("secondary workspace dir survives removeWorkspace: err=%v", err)
	}

	// jj's own listing MUST no longer surface the workspace name.
	// Any residual "feature" mention means the forget never ran.
	cmd := exec.Command("jj", "-R", root, "workspace", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("jj workspace list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "feature") {
		t.Errorf("jj still lists feature workspace:\n%s", out)
	}
}

// TestJJBackendRemoveWorkspaceIdempotent pins the "already gone"
// branch mirroring the git version: a target that jj never knew
// about MUST NOT error — callers rely on this to reconcile stale
// state without a preflight lookup.
func TestJJBackendRemoveWorkspaceIdempotent(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	nonexistent := filepath.Join(parent, "never-was")
	if err := (jjBackend{}).removeWorkspace(root, nonexistent, false, nil); err != nil {
		t.Errorf("idempotent jj removeWorkspace of missing target: %v", err)
	}
}

// TestJJBackendRemoveWorkspaceFiresProgress pins the byte-count
// plumbing: a workspace with a seeded file MUST invoke the
// onProgress callback at least once with the file's size while
// removeWorkspace is sweeping the working-copy directory.
// Guards the split between "jj workspace forget" (metadata-only)
// and executor.RemoveAllProgress (byte-count sweep).
func TestJJBackendRemoveWorkspaceFiresProgress(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initColocatedJJRepo(t, root)

	feature := filepath.Join(parent, "feature")
	if err := (jjBackend{}).createWorkspace(root, feature, "", nil); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}
	// Fixed-size seed so the lower-bound assertion is stable.
	if err := os.WriteFile(filepath.Join(feature, "seed.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	var total int64
	if err := (jjBackend{}).removeWorkspace(root, feature, false, func(n int64) {
		total += n
	}); err != nil {
		t.Fatalf("removeWorkspace: %v", err)
	}
	if total < 2048 {
		t.Errorf("progress total = %d, want >= 2048 (seed.bin size)", total)
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("workspace dir survives removeWorkspace: %v", err)
	}
}

// TestJJBackendUncommittedCountClean pins that a fresh workspace
// with no working-copy changes reports zero uncommitted files. A
// probe failure at this stage would surface the underlying error.
func TestJJBackendUncommittedCountClean(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	initColocatedJJRepo(t, root)

	count, err := (jjBackend{}).uncommittedCount(root)
	if err != nil {
		t.Fatalf("uncommittedCount: %v", err)
	}
	if count != 0 {
		t.Errorf("clean workspace count = %d, want 0", count)
	}
}

// TestJJBackendUncommittedCountDirty pins that a workspace whose @
// change has a modified/new file relative to its parent surfaces a
// non-zero count. The exact value MUST be 1 for a single new file
// so the plan builder can propagate the count into its refusal
// message verbatim.
func TestJJBackendUncommittedCountDirty(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	initColocatedJJRepo(t, root)

	if err := os.WriteFile(
		filepath.Join(root, "file.txt"),
		[]byte("modified"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	count, err := (jjBackend{}).uncommittedCount(root)
	if err != nil {
		t.Fatalf("uncommittedCount: %v", err)
	}
	if count != 1 {
		t.Errorf("dirty workspace count = %d, want 1", count)
	}
}

// TestJJBackendUncommittedCountProbeFailure pins that pointing the
// probe at a directory with no `.jj` surfaces the underlying error
// instead of silently reporting 0 — otherwise a probe failure would
// be indistinguishable from a clean workspace, and the plan builder
// would suppress a refusal it should surface.
func TestJJBackendUncommittedCountProbeFailure(t *testing.T) {
	skipIfNoJJ(t)

	_, err := (jjBackend{}).uncommittedCount(t.TempDir())
	if err == nil {
		t.Fatal("uncommittedCount on non-jj dir: want error, got nil")
	}
}
