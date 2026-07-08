package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/repository"
)

// skipIfNoGit skips the current test when the git binary is not on PATH.
// Every integration test that shells out to git uses it so a minimal
// container without git does not turn engine coverage red.
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// isolateGitConfig strips the developer's git identity and points
// GIT_CONFIG_* at /dev/null so a fresh repo can `git commit` without
// depending on the caller's ~/.gitconfig.
//
// Callers MUST NOT use t.Parallel() — t.Setenv is process-global.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "wrk test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@wrk.local")
	t.Setenv("GIT_COMMITTER_NAME", "wrk test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@wrk.local")
}

// newTestRepoWithHead returns a Repository rooted at a fresh temp dir
// with an initialized git backend, a stable initial commit, and any
// files in tracked so worktrees created from it inherit them.
//
// `git worktree add` refuses a repo with no HEAD, so integration tests
// that create additional workspaces MUST use this variant instead of
// the empty-repo helper.
//
// tracked maps repository-relative paths to their content; every entry
// is written, `git add`ed, and included in the initial commit. Pass nil
// to commit just a README placeholder.
func newTestRepoWithHead(
	t *testing.T,
	tracked map[string]string,
) *repository.Repository {
	t.Helper()
	skipIfNoGit(t)
	isolateGitConfig(t)

	dir := canonPath(t, t.TempDir())

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// -b main pins the branch name so the test isn't sensitive to the
	// caller's init.defaultBranch — isolateGitConfig has already
	// zeroed the global config, but the explicit flag is defensive.
	runGit("init", "-q", "-b", "main")

	if tracked == nil {
		tracked = map[string]string{"README": "wrk-test\n"}
	}

	for rel, content := range tracked {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit("add", "--", rel)
	}
	// Ensure the commit always has something (empty tracked map is
	// covered by the README default above, but be paranoid).
	runGit("commit", "-q", "--allow-empty", "-m", "init")

	repo, err := repository.Detect(dir, repository.Auto)
	if err != nil {
		t.Fatalf("repository.Detect: %v", err)
	}
	return repo
}

// addGitWorktree runs `git worktree add <path>` in primary and returns
// the canonicalized destination path along with the Repository rooted
// there. `path` is relative to the primary's parent directory (i.e.
// wrk's sibling-default convention).
func addGitWorktree(t *testing.T, primary *repository.Repository, name string) (string, *repository.Repository) {
	t.Helper()

	parent := filepath.Dir(primary.Root)
	dest := filepath.Join(parent, name)

	cmd := exec.Command("git", "worktree", "add", "-q", "--", dest)
	cmd.Dir = primary.Root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", dest, err, out)
	}

	dest = canonPath(t, dest)
	repo, err := repository.Detect(dest, repository.Auto)
	if err != nil {
		t.Fatalf("repository.Detect(%s): %v", dest, err)
	}
	return dest, repo
}

// canonPath resolves symlinks so comparisons stay honest on macOS,
// where t.TempDir() sits under /var (a symlink into /private).
func canonPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

// storageIn returns a shared-storage path rooted under the repo's own
// workspace. This matches the convention used by cmd/wrk's end-to-end
// tests (see cmd/wrk/main_test.go storagePath): the executor's
// containment check follows symlinks up from every action path, so a
// symlink from workspace/.env → out-of-workspace-storage/.env resolves
// outside the workspace and trips the "escapes workspace root" guard.
// Placing storage under the repo keeps every resolved target inside
// root and lets the tests exercise the full link→detach→relink cycle.
//
// The directory is created eagerly so callers can hand the returned
// path straight to Options.StorageRoot.
func storageIn(t *testing.T, repoRoot string) string {
	t.Helper()
	dir := filepath.Join(repoRoot, ".wrk-storage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// storageOutside returns a shared-storage path in a completely separate
// t.TempDir() from the repo — the two trees have no ancestor in common,
// mirroring the XDG-style location cmd/wrk picks by default in
// production (under $XDG_STATE_HOME or ~/.local/state, always outside
// any repository root).
//
// Until the leaf-symlink fix in internal/executor/contain.go, this
// path would trip the containment guard: the executor's canonicalize
// resolved symlinks all the way through the leaf, so a workspace-side
// symlink like `<repo>/.env → <external-storage>/…/.env` looked like
// an "escapes workspace root" refusal. The fix stops dereferencing
// the leaf so Detach and Symlink actions can legitimately replace a
// symlink already pointing into shared storage that lives outside
// the workspace.
//
// The returned path is canonicalized so downstream string comparisons
// against the exact bytes wrk writes as a symlink target (e.g.
// os.Readlink output) stay honest on macOS, where t.TempDir() sits
// under /var — itself a symlink into /private/var.
func storageOutside(t *testing.T) string {
	t.Helper()
	return canonPath(t, t.TempDir())
}
