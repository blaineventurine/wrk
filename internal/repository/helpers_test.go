package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// skipIfNoGit skips the current test when the git binary is not on
// PATH. Every real-integration test uses it so a machine without git
// (minimal CI containers, Alpine, etc.) does not fail on unrelated
// coverage.
func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// skipIfNoJJ skips the current test when the jj binary is not on PATH.
func skipIfNoJJ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}
}

// isolateGitConfig points GIT_CONFIG_GLOBAL / GIT_CONFIG_SYSTEM at
// /dev/null and sets a stable committer identity, so no test depends
// on the developer's ~/.gitconfig, /etc/gitconfig, or an ambient
// user.name / user.email.
//
// Uses t.Setenv (not cmd.Env) because the code under test invokes git
// via `passthrough`, which inherits the parent process env. Callers
// therefore MUST NOT use t.Parallel().
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "wrk test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@wrk.local")
	t.Setenv("GIT_COMMITTER_NAME", "wrk test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@wrk.local")
}

// isolateJJConfig points XDG_CONFIG_HOME at an empty temp dir so jj
// does NOT load the caller's ~/.config/jj/config.toml (which may
// reference bookmarks or aliases that do not exist in a fresh repo
// and would poison stderr).
//
// jj shells out to git for colocation, so git isolation is applied
// too. Callers MUST NOT use t.Parallel().
func isolateJJConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	isolateGitConfig(t)
}

// initGitRepo runs `git init -b main` in dir and creates one initial
// commit. `git worktree add` refuses a repository with no HEAD, so
// tests that exercise createWorkspace MUST have a commit.
//
// Callers should first invoke isolateGitConfig(t) to keep the commit
// reproducible.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// -b main pins the initial branch name so tests do not depend on
	// the caller's init.defaultBranch config (isolateGitConfig has
	// already stripped it, but an explicit flag is self-documenting).
	run("init", "-q", "-b", "main")

	if err := os.WriteFile(
		filepath.Join(dir, "README"),
		[]byte("wrk-test\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-q", "-m", "init")
}

// initColocatedJJRepo runs `jj git init --colocate` in dir. The
// resulting repo has both .jj and .git, satisfying jjBackend.commonDir
// and detectVCS's colocated preference.
//
// Callers should first invoke isolateJJConfig(t).
func initColocatedJJRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("jj", "git", "init", "--colocate")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj git init --colocate: %v\n%s", err, out)
	}
}

// canonPath returns the symlink-resolved absolute form of path. On
// macOS t.TempDir() sits under /var/folders (a symlink into /private),
// while VCS tools report the /private form; canonicalizing both sides
// keeps comparisons honest.
func canonPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

// makeDir creates dir (and any missing parents) or fails the test.
func makeDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
