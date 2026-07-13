package engine

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildRemovePlanEmptyDestinationErrors pins the first refusal
// tier: an empty string collapses to the primary root without any
// input from the user, so it MUST be rejected with the same wording
// ResolveDestination uses for symmetry with `wrk new`.
func TestBuildRemovePlanEmptyDestinationErrors(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	_, err := BuildRemovePlan(repo, "", Options{})
	if err == nil {
		t.Fatal("expected error for empty destination")
	}
	if !strings.Contains(err.Error(), "destination cannot be") {
		t.Errorf("err = %v, want to mention 'destination cannot be'", err)
	}
}

// TestBuildRemovePlanDotErrors and ...DotDot pin the other two
// sentinels — "." is the primary, ".." is its parent, both collapse
// to something the user did not mean.
func TestBuildRemovePlanDotErrors(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	_, err := BuildRemovePlan(repo, ".", Options{})
	if err == nil {
		t.Fatal("expected error for .")
	}
}

func TestBuildRemovePlanDotDotErrors(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	_, err := BuildRemovePlan(repo, "..", Options{})
	if err == nil {
		t.Fatal("expected error for ..")
	}
}

// TestBuildRemovePlanPrimaryWorkspaceErrors: removing the primary
// worktree destroys the anchor every other workspace hangs off of,
// so it is a hard error (--force does NOT override).
func TestBuildRemovePlanPrimaryWorkspaceErrors(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	_, err := BuildRemovePlan(repo, repo.Root, Options{})
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("expected 'primary' error, got %v", err)
	}
}

// TestBuildRemovePlanCurrentWorkspaceErrors: if the caller's cwd IS
// the target, running the VCS teardown would pull the ground out
// from under the running process. Refuse before the plan is built.
//
// On macOS t.TempDir() is under /var (symlink into /private), so
// os.Getwd() may report either form depending on how we chdir'd in.
// The check normalises both sides via filepath.EvalSymlinks to keep
// this comparison honest.
func TestBuildRemovePlanCurrentWorkspaceErrors(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(feature); err != nil {
		t.Fatal(err)
	}

	_, err = BuildRemovePlan(repo, feature, Options{})
	if err == nil || !strings.Contains(err.Error(), "current") {
		t.Fatalf("expected 'current' error, got %v", err)
	}
}

// TestBuildRemovePlanUnknownDestinationErrors: a path that is neither
// in Workspaces() nor has a stranded registry entry is genuinely
// unknown — no plan to build, no ghost to point at.
func TestBuildRemovePlanUnknownDestinationErrors(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	_, err := BuildRemovePlan(repo, "/nonexistent/never-a-worktree-xyz-123", Options{})
	if err == nil || !strings.Contains(err.Error(), "not a live workspace") {
		t.Fatalf("expected 'not a live workspace' error, got %v", err)
	}
}

// TestBuildRemovePlanGhostFromRegistry: when the target isn't in
// Workspaces() but the detach registry still has an entry keyed by
// it, we know this was a workspace someone tore down externally.
// The plan returns a Refusal that routes the user to `wrk gc` instead
// of leaking a hard error.
func TestBuildRemovePlanGhostFromRegistry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	reg, err := loadRegistry(repo)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	ghost := "/tmp/definitely-not-a-live-worktree-2626-07-09"
	reg[ghost] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	plan, err := BuildRemovePlan(repo, ghost, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan(ghost): %v", err)
	}
	if !plan.IsGhost {
		t.Errorf("IsGhost = false, want true")
	}
	if !strings.Contains(plan.Refusal, "wrk gc") {
		t.Errorf("Refusal = %q, missing 'wrk gc' hint", plan.Refusal)
	}
}

// TestBuildRemovePlanLiveWorkspaceCleanPlan pins the happy path: a
// real secondary workspace, no uncommitted changes, no detach entry.
// Refusal empty, Backend populated, VCSCommand mentions the git
// worktree command the executor will run.
func TestBuildRemovePlanLiveWorkspaceCleanPlan(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if plan.Refusal != "" {
		t.Errorf("clean workspace should have no refusal, got %q", plan.Refusal)
	}
	if plan.IsGhost {
		t.Errorf("IsGhost = true, want false")
	}
	if plan.Backend != "git" {
		t.Errorf("Backend = %q, want git", plan.Backend)
	}
	if !strings.Contains(plan.VCSCommand, "git worktree remove") {
		t.Errorf("VCSCommand = %q, want to contain 'git worktree remove'", plan.VCSCommand)
	}
	if plan.UncommittedChanges != 0 {
		t.Errorf("UncommittedChanges = %d, want 0", plan.UncommittedChanges)
	}
	if len(plan.DetachedPaths) != 0 {
		t.Errorf("DetachedPaths = %v, want empty", plan.DetachedPaths)
	}
	// Target should be a canonical, absolute path.
	if !filepath.IsAbs(plan.Target) {
		t.Errorf("Target = %q, want absolute", plan.Target)
	}
}

// TestBuildRemovePlanDetachedFilesRefuse: a workspace with detach
// registry entries surfaces a soft refusal — the CLI decides how to
// route --force. DetachedPaths carries the exact list so the print
// layer can render `<path1>, <path2>`.
func TestBuildRemovePlanDetachedFilesRefuse(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	reg, err := loadRegistry(repo)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	reg[feature] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if plan.Refusal == "" {
		t.Fatal("expected refusal for detached files")
	}
	if !strings.Contains(plan.Refusal, "independent local copies") {
		t.Errorf("Refusal = %q, want mention of independent copies", plan.Refusal)
	}
	if len(plan.DetachedPaths) != 1 || plan.DetachedPaths[0] != "node_modules" {
		t.Errorf("DetachedPaths = %v, want [node_modules]", plan.DetachedPaths)
	}
	if plan.IsGhost {
		t.Errorf("IsGhost = true, want false (path IS live)")
	}
}

// TestBuildRemovePlanUncommittedChangesRefuse: an untracked file in
// the secondary workspace trips `git status --porcelain`. The count
// is recorded on the plan and mentioned in the Refusal string; the
// CLI (Task 2.4) decides how to combine it with --force semantics.
func TestBuildRemovePlanUncommittedChangesRefuse(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	// Untracked file: `git status --porcelain` emits one line.
	if err := os.WriteFile(filepath.Join(feature, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if plan.UncommittedChanges < 1 {
		t.Errorf("UncommittedChanges = %d, want >= 1", plan.UncommittedChanges)
	}
	if plan.Refusal == "" || !strings.Contains(plan.Refusal, "uncommitted") {
		t.Errorf("Refusal = %q, want mention of uncommitted changes", plan.Refusal)
	}
}

// TestBuildRemovePlanJJUncommittedChangesRefuse pins the jj branch
// of BuildRemovePlan's uncommitted-changes probe. Before the
// backend-agnostic dispatch through Repository.UncommittedCount,
// this branch was git-only and jj workspaces slipped past the
// safety gate silently. Regression: a modified file in the
// secondary jj workspace MUST surface a non-zero count on the plan
// AND fire the "uncommitted" refusal string.
func TestBuildRemovePlanJJUncommittedChangesRefuse(t *testing.T) {
	repo := newTestColocatedJJRepo(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	// Untracked file: `jj diff --summary` reports it as an added
	// file in the @ change against its parent, one line of output.
	if err := os.WriteFile(filepath.Join(feature, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if plan.UncommittedChanges < 1 {
		t.Errorf("UncommittedChanges = %d, want >= 1", plan.UncommittedChanges)
	}
	if plan.Refusal == "" || !strings.Contains(plan.Refusal, "uncommitted") {
		t.Errorf("Refusal = %q, want mention of uncommitted changes", plan.Refusal)
	}
}

// TestBuildRemovePlanBareNameResolvesSibling: a bare name is treated
// as a sibling of the primary, matching wrk new's convention. Once
// resolved, the same live-workspace/refusal machinery runs.
func TestBuildRemovePlanBareNameResolvesSibling(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}

	plan, err := BuildRemovePlan(repo, "feature", Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan(bare): %v", err)
	}
	if plan.Refusal != "" {
		t.Errorf("clean bare-name plan has Refusal %q, want empty", plan.Refusal)
	}
	wantTarget := filepath.Join(filepath.Dir(repo.Root), "feature")
	if canon, err := filepath.EvalSymlinks(wantTarget); err == nil {
		wantTarget = canon
	}
	if plan.Target != wantTarget {
		t.Errorf("Target = %q, want %q (sibling of primary)", plan.Target, wantTarget)
	}
}

// TestBuildRemovePlanTotalBytesCountsRegularFiles pins the
// TotalBytes population contract: BuildRemovePlan MUST sum every
// regular file under the target. A workspace with two files
// totalling 300 bytes should surface plan.TotalBytes >= 300 (git's
// own .git directory adds more, so we only assert a lower bound).
func TestBuildRemovePlanTotalBytesCountsRegularFiles(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	// Fixed byte counts so the assertion is stable.
	if err := os.WriteFile(filepath.Join(feature, "a.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "b.bin"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if plan.TotalBytes < 300 {
		t.Errorf("TotalBytes = %d, want >= 300 (100 + 200 + git metadata)", plan.TotalBytes)
	}
}

// TestBuildRemovePlanTotalBytesGhostIsZeroSafe pins the ghost
// branch: a ghost target (missing dir, stale registry) still
// returns a plan. TotalBytes will be 0 because treeSize tolerates a
// missing root — the CLI's bar suppression via Threshold handles the
// rest so no spurious render fires.
func TestBuildRemovePlanTotalBytesGhostIsZeroSafe(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	reg, _ := loadRegistry(repo)
	ghost := "/tmp/definitely-not-a-live-worktree-progress-test"
	reg[ghost] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRemovePlan(repo, ghost, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if !plan.IsGhost {
		t.Errorf("IsGhost = false, want true")
	}
	if plan.TotalBytes != 0 {
		t.Errorf("TotalBytes = %d, want 0 for missing target", plan.TotalBytes)
	}
}

// TestExecuteRemoveInvokesProgress pins the plumbing: on a jj
// backend workspace, ExecuteRemove MUST fire Options.Progress at
// least once with a positive byte count. The git backend delegates
// deletion to `git worktree remove`'s own subprocess so the
// callback stays silent there; TestExecuteRemoveProgressSilentOnGit
// pins that half of the contract.
func TestExecuteRemoveInvokesProgress(t *testing.T) {
	repo := newTestColocatedJJRepo(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	// Seed a file the sweep must count. Size well above zero so a
	// spurious zero-byte callback couldn't mask a broken plumbing.
	if err := os.WriteFile(filepath.Join(feature, "seed.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}

	var total int64
	opts := Options{Progress: func(n int64) { total += n }}
	if err := ExecuteRemove(repo, plan, false, opts); err != nil {
		t.Fatalf("ExecuteRemove: %v", err)
	}
	if total < 4096 {
		t.Errorf("progress total = %d, want >= 4096 (seed.bin size)", total)
	}
}

// TestExecuteRemoveProgressSilentOnGit is the git-side mirror of
// TestExecuteRemoveInvokesProgress. `git worktree remove` runs its
// own subprocess and we cannot inspect its deletes; the callback
// MUST stay quiet — but the workspace MUST still be gone.
func TestExecuteRemoveProgressSilentOnGit(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	var fired bool
	opts := Options{Progress: func(int64) { fired = true }}
	if err := ExecuteRemove(repo, plan, false, opts); err != nil {
		t.Fatalf("ExecuteRemove: %v", err)
	}
	if fired {
		t.Errorf("git backend fired Progress callback; expected silent")
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("feature dir survives: %v", err)
	}
}
// TestExecuteRemoveHappyPath: ExecuteRemove tears down a clean
// workspace and the directory is removed from the filesystem.
func TestExecuteRemoveHappyPath(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}

	if err := ExecuteRemove(repo, plan, false, Options{}); err != nil {
		t.Fatalf("ExecuteRemove: %v", err)
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("feature dir survives: %v", err)
	}
}

// TestExecuteRemoveClearsRegistryEntry: ExecuteRemove deletes the
// target from the detach registry, even when --force is required.
func TestExecuteRemoveClearsRegistryEntry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	// Seed a detach entry so the executor has something to clear.
	reg, _ := loadRegistry(repo)
	reg[feature] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		// Detach entries typically produce a soft refusal; but the plan
		// still returns; --force override is Task 2.4's job. Check by
		// dropping the Refusal for this test setup or by passing force
		// through here directly.
		t.Fatalf("BuildRemovePlan: %v", err)
	}

	if err := ExecuteRemove(repo, plan, true, Options{}); err != nil {
		t.Fatalf("ExecuteRemove --force: %v", err)
	}

	after, _ := loadRegistry(repo)
	if _, ok := after[feature]; ok {
		t.Errorf("registry entry survived: %v", after)
	}
}

// TestExecuteRemoveIdempotentAfterExternalRemoval: ExecuteRemove
// idempotently clears the registry entry even when the worktree was
// already removed externally.
func TestExecuteRemoveIdempotentAfterExternalRemoval(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	// Seed a registry entry and externally remove the worktree.
	reg, _ := loadRegistry(repo)
	reg[feature] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", repo.Root, "worktree", "remove", "--force", feature)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}

	// Plan may treat this as a ghost or still find registry state; we
	// construct the plan by hand to isolate the executor test.
	plan := RemovePlan{Target: feature, Backend: "git"}
	if err := ExecuteRemove(repo, plan, true, Options{}); err != nil {
		t.Fatalf("ExecuteRemove idempotent path: %v", err)
	}

	after, _ := loadRegistry(repo)
	if _, ok := after[feature]; ok {
		t.Errorf("registry entry survived idempotent execute: %v", after)
	}
}

// TestBuildRemovePlanPrimaryWorkspaceCarriesTypedErrorCode pins that
// the "refusing to remove the primary workspace" refusal is a typed
// *Error whose Code is ErrPrimaryWorkspace — CLI --json output routes
// on this to give agents a durable knob for recovery logic.
func TestBuildRemovePlanPrimaryWorkspaceCarriesTypedErrorCode(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	// The primary is repo.Root itself.
	_, err := BuildRemovePlan(repo, repo.Root, Options{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "primary workspace") {
		t.Errorf("message = %q, want to contain 'primary workspace'", err.Error())
	}
	var wrkErr *Error
	if !errors.As(err, &wrkErr) {
		t.Fatalf("expected *engine.Error, got %T: %v", err, err)
	}
	if wrkErr.Code != ErrPrimaryWorkspace {
		t.Errorf("code = %q, want %q", wrkErr.Code, ErrPrimaryWorkspace)
	}
}

// TestBuildRemovePlanUnknownDestinationCarriesTypedErrorCode pins the
// ErrNotLiveWorkspace code for a path that is neither in Workspaces()
// nor a ghost.
func TestBuildRemovePlanUnknownDestinationCarriesTypedErrorCode(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	_, err := BuildRemovePlan(repo, "/nonexistent/never-a-worktree-xyz-123", Options{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var wrkErr *Error
	if !errors.As(err, &wrkErr) {
		t.Fatalf("expected *engine.Error, got %T: %v", err, err)
	}
	if wrkErr.Code != ErrNotLiveWorkspace {
		t.Errorf("code = %q, want %q", wrkErr.Code, ErrNotLiveWorkspace)
	}
}

// TestBuildRemovePlanIsolatedVariantsRefuse: a workspace with
// isolation-registry entries surfaces a soft refusal — removing the
// workspace orphans the isolated variants, whose content hooks cannot
// reproduce, and the next `wrk gc` sweeps it. IsolatedPaths carries
// the exact sorted list for the print layer.
func TestBuildRemovePlanIsolatedVariantsRefuse(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	if err := recordIsolation(repo, feature, "node_modules", "/store/x/isolated-abc"); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if len(plan.IsolatedPaths) != 1 || plan.IsolatedPaths[0] != "node_modules" {
		t.Errorf("IsolatedPaths = %v, want [node_modules]", plan.IsolatedPaths)
	}
	if plan.Refusal == "" {
		t.Fatal("expected refusal for isolated variants")
	}
	if !strings.Contains(plan.Refusal, "isolated") {
		t.Errorf("Refusal = %q, want mention of 'isolated'", plan.Refusal)
	}
}

// TestExecuteRemoveClearsIsolationEntries: after a forced removal the
// isolation registry must no longer carry the target's entries — a
// stale entry would keep pinning a variant no workspace references
// until the next gc's orphan sweep.
func TestExecuteRemoveClearsIsolationEntries(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	if err := recordIsolation(repo, feature, "node_modules", "/store/x/isolated-abc"); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if err := ExecuteRemove(repo, plan, true, Options{}); err != nil {
		t.Fatalf("ExecuteRemove --force: %v", err)
	}

	iso, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := iso[feature]; ok {
		t.Errorf("isolation entries survived removal: %v", iso[feature])
	}
}

// TestBuildRemovePlanNoIsolationNoRefusal is the regression guard
// against over-refusing: a clean workspace with no isolation entries
// must produce an empty Refusal and no IsolatedPaths.
func TestBuildRemovePlanNoIsolationNoRefusal(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", "", Options{StorageRoot: storageIn(t, repo.Root),
		Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}
	if plan.Refusal != "" {
		t.Errorf("clean workspace should have no refusal, got %q", plan.Refusal)
	}
	if len(plan.IsolatedPaths) != 0 {
		t.Errorf("IsolatedPaths = %v, want empty", plan.IsolatedPaths)
	}
}

// TestBuildRemovePlanNotLiveHintsAtLeftoverDir pins the G4 hint split:
// when the not-live target EXISTS on disk, the hint routes the user at
// the leftover directory (interrupted-removal fingerprint); when it
// does not exist, the generic "wrk workspaces" hint stays. The message
// string is identical in both cases.
func TestBuildRemovePlanNotLiveHintsAtLeftoverDir(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	leftover := filepath.Join(filepath.Dir(repo.Root), "leftover-dir")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := BuildRemovePlan(repo, leftover, Options{})
	if err == nil {
		t.Fatal("expected error for a non-workspace directory")
	}
	var wrkErr *Error
	if !errors.As(err, &wrkErr) {
		t.Fatalf("expected *engine.Error, got %T: %v", err, err)
	}
	if wrkErr.Code != ErrNotLiveWorkspace {
		t.Errorf("code = %q, want %q", wrkErr.Code, ErrNotLiveWorkspace)
	}
	if !strings.Contains(wrkErr.Hint, "remove it manually") {
		t.Errorf("Hint = %q, want leftover-directory guidance", wrkErr.Hint)
	}

	// Counter: a target that does NOT exist on disk keeps the
	// generic listing hint.
	_, err = BuildRemovePlan(repo, "/nonexistent/never-a-worktree-xyz-123", Options{})
	if err == nil {
		t.Fatal("expected error for a nonexistent target")
	}
	if !errors.As(err, &wrkErr) {
		t.Fatalf("expected *engine.Error, got %T: %v", err, err)
	}
	if !strings.Contains(wrkErr.Hint, "wrk workspaces") {
		t.Errorf("Hint = %q, want the generic 'wrk workspaces' hint", wrkErr.Hint)
	}
}
