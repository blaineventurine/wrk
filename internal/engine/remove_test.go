package engine

import (
	"bytes"
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
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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

// TestBuildRemovePlanBareNameResolvesSibling: a bare name is treated
// as a sibling of the primary, matching wrk new's convention. Once
// resolved, the same live-workspace/refusal machinery runs.
func TestBuildRemovePlanBareNameResolvesSibling(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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

// TestExecuteRemoveHappyPath: ExecuteRemove tears down a clean
// workspace and the directory is removed from the filesystem.
func TestExecuteRemoveHappyPath(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")

	plan, err := BuildRemovePlan(repo, feature, Options{})
	if err != nil {
		t.Fatalf("BuildRemovePlan: %v", err)
	}

	if err := ExecuteRemove(repo, plan, false); err != nil {
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
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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

	if err := ExecuteRemove(repo, plan, true); err != nil {
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
	if err := NewWorkspace(repo, "feature", Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	}); err != nil {
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
	if err := ExecuteRemove(repo, plan, true); err != nil {
		t.Fatalf("ExecuteRemove idempotent path: %v", err)
	}

	after, _ := loadRegistry(repo)
	if _, ok := after[feature]; ok {
		t.Errorf("registry entry survived idempotent execute: %v", after)
	}
}
