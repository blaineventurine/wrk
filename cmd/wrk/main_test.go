package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// wrkBinary is the compiled `wrk` binary shared by every integration
// test in this file. It is built once via buildWrkBinary and torn down
// in TestMain so parallel subtests don't race a re-build.
var (
	wrkBinaryOnce sync.Once
	wrkBinary     string
	wrkBinaryErr  error
)

// buildWrkBinary compiles the current package into a temp binary and
// returns its path. Kept lazy so tests that don't need the binary
// (color, hasProblems) still run when go is unavailable.
func buildWrkBinary(t *testing.T) string {
	wrkBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wrk-bin-*")
		if err != nil {
			wrkBinaryErr = err
			return
		}
		name := "wrk"
		if runtime.GOOS == "windows" {
			name = "wrk.exe"
		}
		out := filepath.Join(dir, name)

		// go build ./cmd/wrk — run from the repo root so relative
		// imports resolve regardless of where the test binary lives.
		root, err := repoRoot()
		if err != nil {
			wrkBinaryErr = err
			return
		}
		// Build with `-cover` when the parent test process was invoked
		// with `go test -cover`. In that mode Go sets GOCOVERDIR to a
		// temp directory that the subprocess inherits, and the
		// instrumented binary writes coverage counters there — Go's test
		// tooling then merges them into the top-level profile before
		// exit. Without GOCOVERDIR, plain `go test ./cmd/wrk` still
		// works because the `-cover` build flag is omitted and no
		// warning is printed to test output.
		buildArgs := []string{"build"}
		if os.Getenv("GOCOVERDIR") != "" {
			buildArgs = append(buildArgs, "-cover", "-coverpkg=./cmd/wrk")
		}
		buildArgs = append(buildArgs, "-o", out, "./cmd/wrk")
		cmd := exec.Command("go", buildArgs...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			wrkBinaryErr = fmt.Errorf("go build: %v\n%s", err, stderr.String())
			return
		}
		wrkBinary = out
	})
	if wrkBinaryErr != nil {
		t.Fatalf("build wrk: %v", wrkBinaryErr)
	}
	return wrkBinary
}

// repoRoot walks up from this test file until it finds a go.mod. The
// go tool sets the working directory to the package under test, so
// `pwd` here is cmd/wrk; walking up two levels lands on the module
// root.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above " + wd)
		}
		dir = parent
	}
}

// runWrk executes the built binary in cwd with args and returns exit
// code, stdout, stderr. Exit code -1 means the process failed to
// start.
func runWrk(t *testing.T, cwd string, args ...string) (int, string, string) {
	bin := buildWrkBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	// Force color off so tabwriter.Escape bytes don't confuse test
	// assertions — the exit-code semantics we're pinning here are
	// independent of ANSI output.
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdout.String(), stderr.String()
	}
	return -1, stdout.String(), stderr.String()
}

// TestStatusExitCodeWithProblems pins U4: `wrk status --exit-code`
// exits 1 when problems exist and prints NO extra error message to
// stderr — the status table above already told the user what's wrong.
func TestStatusExitCodeWithProblems(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	// A plain resource (create defaults to true) with no shared or
	// workspace copy resolves to StateAbsent, which hasProblems treats
	// as a problem — perfect exit-code trigger.
	writeFile(t, filepath.Join(repo, ".wrk.yml"), `
resources:
  - name: env
    path: .env
`)

	code, stdout, stderr := runWrk(t, repo, "status", "--exit-code")
	if code != 1 {
		t.Fatalf(
			"exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr,
		)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf(
			"stderr should be silent for the --exit-code signal, got:\n%s",
			stderr,
		)
	}
	// Sanity: the status table itself was printed to stdout.
	if !strings.Contains(stdout, "env") {
		t.Fatalf("stdout does not look like a status table:\n%s", stdout)
	}
}

// TestStatusRealErrorExitsTwo pins U4: a real error (bad repo, config
// load failure) exits 2 AND prints a message to stderr. That
// separation is what lets pre-commit hooks distinguish an actionable
// linkable state from a broken invocation.
func TestStatusRealErrorExitsTwo(t *testing.T) {
	// Empty temp dir — not a git repo, not a jj repo. currentRepository
	// should fail, so Execute() falls through to the generic error
	// path.
	dir := t.TempDir()

	code, _, stderr := runWrk(t, dir, "status")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatal("stderr should carry the error message on a real error")
	}
}

// TestStatusSuccessExitsZero is the healthy baseline: a repo whose
// only configured resource has State expected (create:false, provided
// out-of-band) is NOT a problem, so --exit-code exits 0.
func TestStatusSuccessExitsZero(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	// No resources configured → nothing to be in a problem state.
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, stdout, stderr := runWrk(t, repo, "status", "--exit-code")
	if code != 0 {
		t.Fatalf(
			"exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr,
		)
	}
}

// TestInitOutsideRepoErrors pins B3: `wrk init` now refuses when
// there is no repository, giving the same "no repository detected"
// class of error as every other command.
func TestInitOutsideRepoErrors(t *testing.T) {
	dir := t.TempDir()

	code, _, stderr := runWrk(t, dir, "init")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatal("expected an error message on stderr")
	}
	// Sanity: no stray .wrk.yml was written in the non-repo dir.
	if _, err := os.Stat(filepath.Join(dir, ".wrk.yml")); err == nil {
		t.Fatalf(".wrk.yml was written into a non-repo directory")
	}
}

// TestRelinkRefusesWithoutYesInNonTTY pins S7: `wrk relink` from a
// non-terminal stdin (which is exactly what `runWrk` sets up — no pty)
// refuses without --yes. The refusal happens before any planning, so
// nothing observable is written to storage.
//
// Exit code 2 (via the top-level error path), stderr names --yes so
// the user knows the escape hatch.
func TestRelinkRefusesWithoutYesInNonTTY(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, _, stderr := runWrk(t, repo, "relink")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %q)", code, stderr)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Fatalf("stderr should mention --yes, got: %q", stderr)
	}
}

// TestRelinkYesAndDryRunCoexist pins S7: --yes and --dry-run are not
// mutually exclusive. --dry-run already bypasses confirmation, and
// piling --yes on top of it MUST still be a legal invocation (exit 0,
// no refusal) — this is how scripts probe the plan while advertising
// that they know the command is destructive.
func TestRelinkYesAndDryRunCoexist(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, _, stderr := runWrk(t, repo, "relink", "--yes", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "refusing") {
		t.Fatalf("--yes --dry-run should not trip the refusal path, got stderr:\n%s", stderr)
	}
}

// TestRelinkDryRunBypassesConfirmation pins S7: `--dry-run` is a
// pure preview — nothing is written, so the confirmation gate does
// not trigger even without --yes and without a TTY. This is what
// makes `wrk relink --dry-run` safe to wire into pre-commit or CI
// as a "would relink change anything?" probe.
func TestRelinkDryRunBypassesConfirmation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")

	code, _, stderr := runWrk(t, repo, "relink", "--dry-run")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "refusing") {
		t.Fatalf("--dry-run should skip confirmation entirely, got stderr:\n%s", stderr)
	}
}

// --- helpers ---

func freshGitRepo(t *testing.T) string {
	t.Helper()
	// EvalSymlinks so downstream code that canonicalizes doesn't
	// disagree with our cwd (macOS /var vs /private/var).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitCommitAll stages every path under repo (including .wrk.yml if
// present) and creates a single commit with the given message. Used
// for tests that call `wrk new`, which is a wrapper around
// `git worktree add`: git needs at least one committed reference for
// the sibling worktree machinery to have something to check out. A
// fresh git init without commits still supports --orphan worktrees,
// but wrk's tests want the child worktree to share the parent's
// tracked config — a normal commit is the simplest way to get that.
func gitCommitAll(t *testing.T, repo, message string) {
	t.Helper()

	env := []string{
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t",
	}

	add := exec.Command("git", "-C", repo, "add", "-A")
	add.Env = append(os.Environ(), env...)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	commit := exec.Command("git", "-C", repo, "commit", "-q", "-m", message)
	commit.Env = append(os.Environ(), env...)
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

// storagePath returns a workspace-contained storage root. The
// executor's containment check follows symlinks up from every action
// path, so a symlink from workspace/.env → external-storage/.env
// resolves outside the workspace and trips the "escapes workspace
// root" guard. Placing storage under the repo keeps every action's
// resolved target inside the root and lets tests exercise the full
// link → detach → relink cycle end-to-end.
func storagePath(repo string) string {
	return filepath.Join(repo, ".storage")
}

// TestInitWritesValidConfig pins the `wrk init` happy path: in a
// canonical git repo with a well-known project file, `wrk init`
// writes a `.wrk.yml` that config.Load parses without error, and the
// stdout hint mentions the detected project kind.
//
// This is the whole point of `init` — the generated file has to be a
// legal config, otherwise every downstream command breaks immediately.
func TestInitWritesValidConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")

	code, stdout, stderr := runWrk(t, repo, "init")
	if code != 0 {
		t.Fatalf("init exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// The generated file exists, and config.Load can parse it.
	target := filepath.Join(repo, ".wrk.yml")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf(".wrk.yml not written: %v", err)
	}
	cfg, err := config.Load(repo)
	if err != nil {
		body, _ := os.ReadFile(target)
		t.Fatalf("generated .wrk.yml does not round-trip through config.Load: %v\ncontent:\n%s", err, body)
	}
	// env.example → an "env" resource in the generated template.
	found := false
	for _, r := range cfg.Resources {
		if r.Name == "env" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("generated config missing 'env' resource; got %+v", cfg.Resources)
	}

	if !strings.Contains(stdout, "env") {
		t.Errorf("stdout should mention the 'env' detection so the user knows what shipped:\n%s", stdout)
	}
}

// TestInitDryRunHasNoSideEffects pins the --dry-run contract: no
// .wrk.yml is written, and the YAML preview is emitted on stdout so
// pipes like `wrk init --dry-run | tee` still work.
func TestInitDryRunHasNoSideEffects(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, "Gemfile"), "")

	code, stdout, stderr := runWrk(t, repo, "init", "--dry-run")
	if code != 0 {
		t.Fatalf("init --dry-run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// Nothing on disk.
	if _, err := os.Stat(filepath.Join(repo, ".wrk.yml")); !os.IsNotExist(err) {
		t.Fatalf(".wrk.yml should not exist after --dry-run, got stat err=%v", err)
	}

	// The preview is on stdout; it should contain YAML-ish text for the
	// bundler detection (Gemfile → bundler snippet).
	if !strings.Contains(stdout, "Would write to:") {
		t.Errorf("stdout should announce the target path preview:\n%s", stdout)
	}
	if !strings.Contains(stdout, "resources") {
		t.Errorf("stdout should carry the YAML preview mentioning 'resources':\n%s", stdout)
	}
}

// TestLinkHappyPathAndIdempotent pins that `wrk link` (a) moves the
// workspace file into shared storage and replaces it with a symlink,
// and (b) a second immediate `wrk link` is a no-op (no plan actions,
// exit 0). Idempotence is what makes `wrk link` a safe pre-commit hook.
func TestLinkHappyPathAndIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "link")
	if code != 0 {
		t.Fatalf("link exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// After link the workspace .env must be a symlink pointing under storage.
	info, err := os.Lstat(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("lstat .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".env should be a symlink after link, got mode=%s", info.Mode())
	}
	target, err := os.Readlink(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("readlink .env: %v", err)
	}
	if !strings.HasPrefix(target, storage) {
		t.Errorf(".env should point under %q, got %q", storage, target)
	}

	// Second link: idempotent — no actions in the printed plan.
	code2, stdout2, stderr2 := runWrk(t, repo, "--storage", storage, "link")
	if code2 != 0 {
		t.Fatalf("second link exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code2, stdout2, stderr2)
	}
	// The plan preview uses "•" for each action line. Zero bullets means
	// zero planned mutations — a genuine no-op.
	if strings.Contains(stdout2, "•") {
		t.Fatalf("second link should be a no-op (no plan bullets), got:\n%s", stdout2)
	}
}

// TestLinkConflictExitsNonZero pins the conflict path: when the
// workspace has both a shared copy and a local real file at the same
// path, `wrk link` refuses (exit 2) and stdout carries the word
// "conflict" so the user knows what to fix. Silent success here would
// clobber user work.
func TestLinkConflictExitsNonZero(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")

	// First link to establish shared storage.
	if code, _, stderr := runWrk(t, repo, "--storage", storage, "link"); code != 0 {
		t.Fatalf("initial link failed: exit=%d stderr=%s", code, stderr)
	}

	// Now replace the symlink with a competing real file to fabricate a
	// conflict. Remove the symlink first so we can write a fresh file
	// at the same path.
	if err := os.Remove(filepath.Join(repo, ".env")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".env"), "LOCAL_EDIT=1\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "link")
	if code == 0 {
		t.Fatalf("link on conflict should not exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(strings.ToLower(combined), "conflict") {
		t.Errorf("conflict message missing; combined output:\n%s", combined)
	}
}

// TestLinkDryRunHasNoSideEffects pins that --dry-run prints the plan
// but leaves the workspace untouched. Signal: `.env` is still a real
// file, not a symlink, after the invocation.
func TestLinkDryRunHasNoSideEffects(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "link", "--dry-run")
	if code != 0 {
		t.Fatalf("link --dry-run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// Plan was printed (non-empty bullet list).
	if !strings.Contains(stdout, "•") {
		t.Errorf("plan preview should carry bullet lines:\n%s", stdout)
	}

	// .env is still a real file.
	info, err := os.Lstat(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("lstat .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".env became a symlink after --dry-run; expected no change")
	}

	// Storage subtree must not exist yet.
	if _, err := os.Stat(storage); !os.IsNotExist(err) {
		t.Fatalf("storage should not be created under --dry-run, got stat err=%v", err)
	}
}

// TestNewFeatureCreatesSiblingWorktree pins the `wrk new feature`
// happy path: given a repo with an initial commit, a sibling worktree
// exists at `<parent>/feature` after the call, and it carries the
// committed `.wrk.yml`. This is the whole promise of `wrk new`.
func TestNewFeatureCreatesSiblingWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	code, stdout, stderr := runWrk(t, repo, "new", "feature")
	if code != 0 {
		t.Fatalf("new feature exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// The sibling worktree exists.
	sibling := filepath.Join(filepath.Dir(repo), "feature")
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("sibling worktree not created at %s: %v", sibling, err)
	}
	// Cleanup for parallel-safety: remove the worktree so the test
	// tempdir cleanup succeeds even though it's a git worktree.
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "-f", sibling).Run()
		_ = os.RemoveAll(sibling)
	})

	if _, err := os.Stat(filepath.Join(sibling, ".wrk.yml")); err != nil {
		t.Fatalf(".wrk.yml missing in the new worktree: %v", err)
	}
}

// TestNewFeatureDryRunHasNoSideEffects pins the --dry-run contract on
// `wrk new`: no sibling directory is created, and stdout announces the
// destination that would be used.
func TestNewFeatureDryRunHasNoSideEffects(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	code, stdout, stderr := runWrk(t, repo, "new", "feature", "--dry-run")
	if code != 0 {
		t.Fatalf("new --dry-run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	sibling := filepath.Join(filepath.Dir(repo), "feature")
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Fatalf("--dry-run should not create %s, got stat err=%v", sibling, err)
	}
	if !strings.Contains(stdout, "Would create workspace") {
		t.Errorf("stdout should announce dry-run destination:\n%s", stdout)
	}
}

// TestNewDotIsRejected pins S8: `wrk new .` is a user mistake — "."
// means the current directory, which IS the current workspace. It
// must be rejected up front with a clear error, not permitted to
// crash the destination-exists check downstream.
func TestNewDotIsRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	code, _, stderr := runWrk(t, repo, "new", ".")
	if code != 2 {
		t.Fatalf("new . exit = %d, want 2 (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, `"."`) {
		t.Errorf("stderr should quote the bad destination for clarity, got: %q", stderr)
	}
}

// TestDetachThenSecondDetachIdempotent is the C1 regression: `wrk
// detach` twice in a row must leave the registry intact after the
// second call. Prior to the fix, the second call could wipe the
// registry entry, so `wrk status` would misclassify a previously-
// detached resource as a conflict. This test asserts the observable
// downstream: `wrk status` after a double detach still shows the
// resource as "detached", not "conflict".
func TestDetachThenSecondDetachIdempotent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")

	if code, _, stderr := runWrk(t, repo, "--storage", storage, "link"); code != 0 {
		t.Fatalf("link failed: exit=%d stderr=%s", code, stderr)
	}
	if code, _, stderr := runWrk(t, repo, "--storage", storage, "detach"); code != 0 {
		t.Fatalf("first detach failed: exit=%d stderr=%s", code, stderr)
	}

	// After first detach: .env is a real file, not a symlink.
	info, err := os.Lstat(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("lstat .env after detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".env should be a real file after detach, got symlink")
	}

	// Second detach: must succeed and preserve the detached record.
	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "detach")
	if code != 0 {
		t.Fatalf("second detach exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// Status must still call it "detached", not "conflict".
	scode, sstdout, sstderr := runWrk(t, repo, "--storage", storage, "status")
	if scode != 0 {
		t.Fatalf("status exit = %d, want 0 (stderr=%s)", scode, sstderr)
	}
	if !strings.Contains(sstdout, "detached") {
		t.Errorf("status should show 'detached' after double detach; got:\n%s", sstdout)
	}
	if strings.Contains(sstdout, "conflict") {
		t.Errorf("double detach must not degrade to 'conflict'; got:\n%s", sstdout)
	}
}

// TestStatusExitCodeUnprovisionedExits1 pins U4 end-to-end: an
// unprovisioned resource in a fresh repo is a "linkable" problem, so
// `wrk status --exit-code` exits 1 and — critically — leaves stderr
// empty. The status table above is the only user-visible message.
// A regression here would either break pre-commit hooks (wrong code)
// or spam CI logs (loud stderr).
func TestStatusExitCodeUnprovisionedExits1(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	// A resource without a local file and without shared storage is
	// "absent" — a canonical linkable problem.
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")

	code, _, stderr := runWrk(t, repo, "status", "--exit-code")
	if code != 1 {
		t.Fatalf("--exit-code on unprovisioned should be 1, got %d (stderr=%q)", code, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr should stay silent for the --exit-code signal, got:\n%s", stderr)
	}
}

// TestListPrintsResourceTable pins the wire between the CLI, engine.List,
// and printList: given a configured resource, `wrk list` produces a
// table that names the RESOURCE, PATH, FINGERPRINTED, VARIANTS, and
// SHARED PATH columns and carries a row for the resource.
func TestListPrintsResourceTable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "list")
	if code != 0 {
		t.Fatalf("list exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// Header spot-check: without --size, no SIZE column.
	header := strings.SplitN(stdout, "\n", 2)[0]
	for _, col := range []string{"RESOURCE", "PATH", "FINGERPRINTED", "VARIANTS", "SHARED PATH"} {
		if !strings.Contains(header, col) {
			t.Errorf("list header missing column %q; got %q", col, header)
		}
	}
	if strings.Contains(header, "SIZE") {
		t.Errorf("SIZE column should be absent without --size; got %q", header)
	}

	// Row for env is present.
	if !strings.Contains(stdout, "env") || !strings.Contains(stdout, ".env") {
		t.Errorf("row for env resource missing; output:\n%s", stdout)
	}
}

// TestWorkspacesAndWSAliasSameOutput pins that the `ws` alias is a
// true alias — it produces exactly the same output as `workspaces` in
// the same repo. If the alias ever drifts (different flag defaults,
// different registration), users typing the short form get a
// different table.
func TestWorkspacesAndWSAliasSameOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")
	if code, _, stderr := runWrk(t, repo, "--storage", storage, "link"); code != 0 {
		t.Fatalf("link setup failed: exit=%d stderr=%s", code, stderr)
	}

	code1, out1, _ := runWrk(t, repo, "--storage", storage, "workspaces")
	code2, out2, _ := runWrk(t, repo, "--storage", storage, "ws")

	if code1 != 0 || code2 != 0 {
		t.Fatalf("workspaces=%d ws=%d, both should be 0", code1, code2)
	}
	if out1 != out2 {
		t.Fatalf("workspaces and ws produced different output\nworkspaces:\n%s\nws:\n%s", out1, out2)
	}
	// Sanity: the current workspace row is present and marked `*`.
	if !strings.Contains(out1, repo) {
		t.Errorf("workspaces output should carry the current repo path %q:\n%s", repo, out1)
	}
	if !strings.Contains(out1, "*") {
		t.Errorf("current workspace should be marked with '*'; output:\n%s", out1)
	}
}

// TestHelpListsAllSubcommands is a shape check on `wrk --help`: every
// user-facing subcommand must appear in the top-level help. A missing
// entry usually means an accidentally-unregistered command — the sort
// of change that ships without any smoke test because the code still
// compiles.
func TestHelpListsAllSubcommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// --help doesn't need a repo, but we set cwd to a temp dir just to
	// avoid emitting output relative to the go test cwd.
	code, stdout, stderr := runWrk(t, t.TempDir(), "--help")
	if code != 0 {
		t.Fatalf("--help exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, sub := range []string{"init", "new", "link", "detach", "relink", "status", "list", "workspaces", "gc", "remove"} {
		if !strings.Contains(stdout, sub) {
			t.Errorf("--help output missing subcommand %q; full help:\n%s", sub, stdout)
		}
	}
}

// TestRelinkYesInNonTTYExecutes pins S7's happy path: --yes in a
// non-TTY context (which is exactly how CI and scripted callers
// invoke it) must proceed through the destructive path. Complements
// TestRelinkRefusesWithoutYesInNonTTY, which pins the refusal.
func TestRelinkYesInNonTTYExecutes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")
	if code, _, stderr := runWrk(t, repo, "--storage", storage, "link"); code != 0 {
		t.Fatalf("link setup failed: exit=%d stderr=%s", code, stderr)
	}
	if code, _, stderr := runWrk(t, repo, "--storage", storage, "detach"); code != 0 {
		t.Fatalf("detach setup failed: exit=%d stderr=%s", code, stderr)
	}

	// Now relink with --yes should proceed and restore the symlink.
	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "relink", "--yes")
	if code != 0 {
		t.Fatalf("relink --yes exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	info, err := os.Lstat(filepath.Join(repo, ".env"))
	if err != nil {
		t.Fatalf("lstat .env after relink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".env should be a symlink after relink --yes, got mode=%s", info.Mode())
	}
}

// TestNewWithAbsolutePathCreatesWorktreeThere pins Medium #10: an
// absolute path is respected literally by ResolveDestination, so
// `wrk new /somewhere/else` places the new git worktree at
// /somewhere/else (NOT next to the primary). Verified end-to-end:
// the directory exists, its `.git` is a linked-worktree gitdir FILE
// (not a directory), and `wrk workspaces` from the primary now
// lists the canonicalized absolute path as a live worktree. The
// workspaces assertion is the load-bearing one — the .git-file
// signature would still hold even if the worktree were somehow
// orphaned from git's metadata.
func TestNewWithAbsolutePathCreatesWorktreeThere(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	// One `create:false` resource so `wrk workspaces` produces a row
	// per worktree — WorkspaceSummaries is built from Status rows, and
	// an empty `resources: []` config yields no rows at all. `create:
	// false` keeps the resource in state `expected` (out-of-band) with
	// no plan actions and no conflict, so the primary Link inside
	// NewWorkspace stays a no-op.
	writeFile(t, filepath.Join(repo, ".wrk.yml"),
		"resources:\n  - name: env\n    path: .env\n    create: false\n")
	gitCommitAll(t, repo, "init")

	// Canonicalize the destination base so it matches `git worktree
	// list --porcelain` output — on macOS t.TempDir() sits under
	// /var/folders/... which is a symlink to /private/var/folders/...
	// and git reports the canonical form.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	absDest := filepath.Join(base, "far-away-feature")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "new", absDest)
	if code != 0 {
		t.Fatalf("new %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			absDest, code, stdout, stderr)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "-f", absDest).Run()
		_ = os.RemoveAll(absDest)
	})

	info, err := os.Stat(absDest)
	if err != nil {
		t.Fatalf("stat abs dest %s: %v", absDest, err)
	}
	if !info.IsDir() {
		t.Fatalf("abs dest %s should be a directory, got mode=%s", absDest, info.Mode())
	}

	// Linked-worktree signature: .git is a FILE containing `gitdir:`.
	dotGit := filepath.Join(absDest, ".git")
	gitInfo, err := os.Stat(dotGit)
	if err != nil {
		t.Fatalf("stat %s: %v (git worktree add did not run)", dotGit, err)
	}
	if gitInfo.IsDir() {
		t.Fatalf(".git in linked worktree is a directory; want a gitdir file")
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		t.Fatalf("read .git file: %v", err)
	}
	if !strings.HasPrefix(string(data), "gitdir:") {
		t.Fatalf(".git should start with `gitdir:`; got %q", string(data))
	}

	// `wrk workspaces` from the primary MUST list the absolute-path
	// worktree. This is what proves git accepted it as a workspace of
	// this repository — not merely that some directory happens to
	// exist at absDest.
	wcode, wout, werr := runWrk(t, repo, "--storage", storage, "workspaces")
	if wcode != 0 {
		t.Fatalf("workspaces exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			wcode, wout, werr)
	}
	if !strings.Contains(wout, absDest) {
		t.Errorf("workspaces output missing the absolute-path worktree %q; got:\n%s",
			absDest, wout)
	}
}

// TestNewAbsolutePathAlreadyExistsRefusesCleanly pins Medium #10's
// unhappy twin: when the target absolute path already exists on
// disk, `wrk new` MUST refuse before invoking git — nothing user-
// authored may be clobbered. The failure is a real error (not the
// exit-code sentinel), so the process exits 2 and stderr names
// "already exists" so the user knows exactly what is in the way.
// Byte-level assertion on the pre-existing marker proves no
// clobber occurred.
func TestNewAbsolutePathAlreadyExistsRefusesCleanly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	absDest := filepath.Join(base, "already-there")
	if err := os.MkdirAll(absDest, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(absDest, "keep.txt")
	writeFile(t, marker, "preserved\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "new", absDest)
	if code != 2 {
		t.Fatalf("new <existing abs> exit = %d, want 2 (real error path)\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr should mention 'already exists'; got:\n%s", stderr)
	}

	// Marker byte-for-byte identical — nothing overwrote it.
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker vanished after refused new: %v", err)
	}
	if string(body) != "preserved\n" {
		t.Errorf("marker content changed: got %q, want %q", string(body), "preserved\n")
	}
	// And no `.git` file was dropped into the pre-existing dir —
	// git worktree add never ran.
	if _, err := os.Stat(filepath.Join(absDest, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should not have been created inside the pre-existing dir; stat err = %v", err)
	}
}

// TestNewExplicitParentRelativeCreatesSibling pins Medium #11:
// `wrk new ../explicit-feature` is the pre-sibling-default calling
// convention that longtime users still type from muscle memory. It
// has a path separator, so ResolveDestination treats it literally
// against the primary root — landing on the exact same sibling
// directory the bare-name policy would have chosen. This test locks
// in that backwards-compat path: the resulting sibling is a real
// linked worktree (`.git` file present, `.wrk.yml` from the initial
// commit rides over) so any regression that turned the explicit
// form into a plain mkdir would flip red.
func TestNewExplicitParentRelativeCreatesSibling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "new", "../explicit-feature")
	if code != 0 {
		t.Fatalf("new ../explicit-feature exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}

	sibling := filepath.Join(filepath.Dir(repo), "explicit-feature")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "-f", sibling).Run()
		_ = os.RemoveAll(sibling)
	})

	info, err := os.Stat(sibling)
	if err != nil {
		t.Fatalf("sibling not created at %s: %v", sibling, err)
	}
	if !info.IsDir() {
		t.Fatalf("sibling %s should be a directory, got mode=%s", sibling, info.Mode())
	}

	// Real linked worktree — .git is a FILE with `gitdir:` prefix.
	dotGit := filepath.Join(sibling, ".git")
	gitInfo, err := os.Stat(dotGit)
	if err != nil {
		t.Fatalf("stat %s: %v", dotGit, err)
	}
	if gitInfo.IsDir() {
		t.Fatalf(".git is a directory; want linked-worktree gitdir file")
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		t.Fatalf("read .git: %v", err)
	}
	if !strings.HasPrefix(string(data), "gitdir:") {
		t.Fatalf(".git should start with `gitdir:`; got %q", string(data))
	}

	// The tracked .wrk.yml rides over from the initial commit — the
	// worktree really is a checkout of primary's HEAD.
	if _, err := os.Stat(filepath.Join(sibling, ".wrk.yml")); err != nil {
		t.Errorf(".wrk.yml missing in sibling worktree: %v", err)
	}
}

// TestNewExplicitSubdirectoryPathIsRejected pins Medium #11's dark
// side: `wrk new ./inside/foo` resolves to a path INSIDE the primary
// workspace root. Nested worktrees confuse both git and jj, and
// wrk's shared-storage design assumes workspaces are siblings — so
// the containment guard MUST reject this before any filesystem
// side effect. Exit code is the real-error 2, stderr carries the
// canonical "inside existing workspace" message, and — critically —
// neither the nested destination NOR the intermediate `inside/`
// directory is created on disk. The guard fires on
// ResolveDestination, before any mkdir.
func TestNewExplicitSubdirectoryPathIsRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "new", "./inside/foo")
	if code != 2 {
		t.Fatalf("new ./inside/foo exit = %d, want 2\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}
	if !strings.Contains(stderr, "inside existing workspace") {
		t.Errorf("stderr should carry the nesting-guard message; got:\n%s", stderr)
	}

	// Neither the nested destination nor the intermediate path may
	// exist — a partial mkdir would fool the next invocation into
	// "already exists" and mask the real reason for the refusal.
	dest := filepath.Join(repo, "inside", "foo")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("nested destination %s should not exist after refused new; stat err = %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "inside")); !os.IsNotExist(err) {
		t.Errorf("intermediate `inside/` directory should not be created; stat err = %v", err)
	}
}

// TestNewBareNameCollidesWithExistingSiblingDirectory pins Medium
// #12: `wrk new feature` from the primary defaults to the sibling
// path <parent>/feature. If a directory already sits there, the
// exists-check MUST refuse — no clobbering, no accidental
// `git worktree add` on top of unrelated user content. Verified by
// seeding the sibling with a marker file and confirming the marker
// survives the refused invocation byte-for-byte, plus asserting
// that no `.git` sentinel was dropped into the pre-existing dir.
func TestNewBareNameCollidesWithExistingSiblingDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	// Seed the sibling that `wrk new feature` would target.
	sibling := filepath.Join(filepath.Dir(repo), "feature")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sibling, "userdata.txt")
	writeFile(t, marker, "do-not-touch\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "new", "feature")
	if code != 2 {
		t.Fatalf("new feature exit = %d, want 2 (colliding sibling)\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr should carry 'already exists'; got:\n%s", stderr)
	}

	// Marker byte-for-byte identical — nothing overwrote it.
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker vanished: %v", err)
	}
	if string(body) != "do-not-touch\n" {
		t.Errorf("marker content changed: got %q, want %q", string(body), "do-not-touch\n")
	}
	// The sibling is still a plain directory — `wrk new` did NOT
	// convert it into a git worktree by dropping a `.git` file.
	if _, err := os.Stat(filepath.Join(sibling, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should not have been created inside the pre-existing sibling; stat err = %v", err)
	}
}

// TestNewSameNameTwiceFailsSecondTime pins Medium #16 via the most-
// reproducible path: the second identical `wrk new` collides with
// the destination that the first one just created. The
// ResolveDestination exists-check catches it BEFORE git runs, so
// the error surfaces as a real error (exit 2, stderr "already
// exists"), NOT the exit-code sentinel. The first worktree — its
// `.git` gitdir file and its `.wrk.yml` — MUST be byte-for-byte
// identical after the refused second call.
//
// NOTE on Medium #16: a raw `git worktree add` failure that ISN'T
// caught by our exists-check (e.g. a branch-name collision from a
// foreign worktree) is not reproducible hermetically from the CLI,
// and the same exists-check catches every user-facing invocation
// in practice. If a future regression removed the exists-check,
// this test still flips red because the second call would then
// succeed and the first worktree would be replaced.
func TestNewSameNameTwiceFailsSecondTime(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	sibling := filepath.Join(filepath.Dir(repo), "feature")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "-f", sibling).Run()
		_ = os.RemoveAll(sibling)
	})

	// First call: succeeds and lays down the linked worktree.
	if code, out, se := runWrk(t, repo, "--storage", storage, "new", "feature"); code != 0 {
		t.Fatalf("first new feature exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, se)
	}
	dotGit := filepath.Join(sibling, ".git")
	firstGit, err := os.ReadFile(dotGit)
	if err != nil {
		t.Fatalf("read .git after first new: %v", err)
	}
	firstCfg, err := os.ReadFile(filepath.Join(sibling, ".wrk.yml"))
	if err != nil {
		t.Fatalf("read .wrk.yml after first new: %v", err)
	}

	// Second call: MUST fail with "already exists" from
	// ResolveDestination — before git ever runs.
	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "new", "feature")
	if code != 2 {
		t.Fatalf("second new feature exit = %d, want 2\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}
	if !strings.Contains(stderr, "already exists") {
		t.Errorf("stderr should mention 'already exists'; got:\n%s", stderr)
	}

	// First worktree is untouched: same .git gitdir contents and
	// same .wrk.yml content, byte-for-byte.
	secondGit, err := os.ReadFile(dotGit)
	if err != nil {
		t.Fatalf("read .git after refused second new: %v", err)
	}
	if !bytes.Equal(firstGit, secondGit) {
		t.Errorf("first worktree's .git changed after refused second new\nbefore: %q\nafter:  %q",
			firstGit, secondGit)
	}
	secondCfg, err := os.ReadFile(filepath.Join(sibling, ".wrk.yml"))
	if err != nil {
		t.Fatalf("read .wrk.yml after refused second new: %v", err)
	}
	if !bytes.Equal(firstCfg, secondCfg) {
		t.Errorf("first worktree's .wrk.yml changed after refused second new\nbefore: %q\nafter:  %q",
			firstCfg, secondCfg)
	}
}

// setupGCFixture stands up a git repo whose sole configured resource
// is a fingerprinted node_modules. It runs `wrk link` twice with a
// different fingerprint input between the two calls, leaving one
// stale variant subdirectory (the v1 one, no longer symlinked from
// any live workspace) alongside the current v2 variant. Returns the
// repo root and the two variant paths in stale/current order so
// callers can assert on-disk mutation.
func setupGCFixture(t *testing.T) (repo, staleVariant, currentVariant string) {
	t.Helper()

	repo = freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"),
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/package.json\"\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'mkdir -p \"{shared}\" && touch \"{shared}/.installed\"'\n",
	)
	writeFile(t, filepath.Join(repo, "package.json"), `{"v":1}`)

	storage := storagePath(repo)

	// Variant 1.
	if code, stdout, stderr := runWrk(t, repo, "--storage", storage, "link"); code != 0 {
		t.Fatalf("link v1 exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// Bump the fingerprint input and remove the current symlink so
	// the second link builds a fresh variant subdir rather than
	// treating the existing symlink as "already correctly linked".
	writeFile(t, filepath.Join(repo, "package.json"), `{"v":2}`)
	if err := os.Remove(filepath.Join(repo, "node_modules")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove node_modules before v2 link: %v", err)
	}

	// Variant 2.
	if code, stdout, stderr := runWrk(t, repo, "--storage", storage, "link"); code != 0 {
		t.Fatalf("link v2 exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	// The current symlink points at the v2 variant; the other entry
	// under node_modules/ is the stale v1 that gc should sweep.
	current, err := os.Readlink(filepath.Join(repo, "node_modules"))
	if err != nil {
		t.Fatalf("readlink node_modules: %v", err)
	}
	currentVariant = current

	nmParent := filepath.Dir(current)
	entries, err := os.ReadDir(nmParent)
	if err != nil {
		t.Fatalf("read variant parent %q: %v", nmParent, err)
	}
	var others []string
	for _, e := range entries {
		// Skip sibling bookkeeping files (.wrk-lock, .wrk-provisioning,
		// .wrk-deleting). Variants are always directories.
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(nmParent, e.Name())
		if p == current {
			continue
		}
		others = append(others, p)
	}
	if len(others) != 1 {
		t.Fatalf("expected exactly one stale variant next to %q, got %v", current, others)
	}
	staleVariant = others[0]
	return repo, staleVariant, currentVariant
}

// TestMainGCDryRunHasNoEffect pins the --dry-run contract on the CLI:
// the plan is printed, exit is 0, and nothing on disk is mutated —
// specifically, the stale variant subdirectory is still present after
// the invocation.
func TestMainGCDryRunHasNoEffect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo, stale, _ := setupGCFixture(t)

	code, stdout, stderr := runWrk(t, repo, "--storage", storagePath(repo), "gc", "--dry-run")
	if code != 0 {
		t.Fatalf("gc --dry-run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	// PrintGCPlan groups variants by resource path (`node_modules:`)
	// and always emits a `Total:` footer when the plan is non-empty.
	// Either missing means the plan was not printed properly.
	if !strings.Contains(stdout, "node_modules") || !strings.Contains(stdout, "Total:") {
		t.Errorf("dry-run output missing plan content:\n%s", stdout)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("stale variant %q should still exist after --dry-run: %v", stale, err)
	}
}

// TestMainGCRefusesNonTTYWithoutYes pins the confirmation gate: a
// non-terminal stdin (which `runWrk` always provides — no pty) with
// no --yes must refuse rather than silently prompt for input nobody
// will type. Exit is non-zero and the stale variant survives.
func TestMainGCRefusesNonTTYWithoutYes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo, stale, _ := setupGCFixture(t)

	code, stdout, stderr := runWrk(t, repo, "--storage", storagePath(repo), "gc")
	if code == 0 {
		t.Fatalf("gc without --yes on non-TTY should not exit 0\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr should mention --yes escape hatch, got: %q", stderr)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("stale variant %q should survive a refused gc: %v", stale, err)
	}
}

// TestMainGCYesDeletesStaleVariant pins the happy path: --yes carries
// the caller past the non-TTY refusal, ExecuteGC runs, and the stale
// variant subdirectory is gone from disk while the current one
// survives.
func TestMainGCYesDeletesStaleVariant(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo, stale, current := setupGCFixture(t)

	code, stdout, stderr := runWrk(t, repo, "--storage", storagePath(repo), "gc", "--yes")
	if code != 0 {
		t.Fatalf("gc --yes exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale variant %q should be gone after gc --yes, stat err = %v", stale, err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Errorf("current variant %q should survive gc --yes: %v", current, err)
	}
}

// setupRemoveFixture stands up a git repo committed to `main` with an
// empty `.wrk.yml`, then invokes the built binary to create a sibling
// worktree named "feature". Returns the primary repo root and the
// absolute feature-workspace path, both canonicalized so downstream
// comparisons against Workspaces() (which canonicalizes /tmp →
// /private/tmp on macOS) stay honest.
func setupRemoveFixture(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	code, stdout, stderr := runWrk(t, repo,
		"--storage", storagePath(repo), "new", "feature")
	if code != 0 {
		t.Fatalf("wrk new feature: exit = %d\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}

	feature := filepath.Join(filepath.Dir(repo), "feature")
	if canon, err := filepath.EvalSymlinks(feature); err == nil {
		feature = canon
	}

	// Parallel-safety: even after the test tempdir cleanup, git may
	// keep a linked-worktree gitdir under repo/.git/worktrees/feature
	// that references the (now-deleted) sibling. Prune it here so
	// re-running the suite in-place doesn't leave orphans behind.
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "-f", feature).Run()
		_ = os.RemoveAll(feature)
	})

	return repo, feature
}

// TestMainRemoveYesDeletesFeature pins the `wrk remove` happy path:
// with --yes carrying past the non-TTY refusal, the sibling feature
// worktree is torn down and no longer present on disk.
func TestMainRemoveYesDeletesFeature(t *testing.T) {
	testDir, feature := setupRemoveFixture(t)

	code, stdout, stderr := runWrk(t, testDir,
		"--storage", storagePath(testDir), "remove", feature, "--yes")
	if code != 0 {
		t.Fatalf("remove --yes exit = %d\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("feature dir should be gone after remove --yes, stat err = %v", err)
	}
}

// TestMainRemoveRefusesPrimary pins the hard-error branch: pointing
// `wrk remove` at the primary workspace (the anchor everything else
// hangs off) must refuse with a clear "primary" message. --yes cannot
// override this; it is a plan-builder error, not a soft refusal.
//
// The command is issued from inside the feature worktree so the
// current-workspace guard (checked BEFORE the primary guard) doesn't
// steal the refusal — this test is specifically about the primary
// check.
func TestMainRemoveRefusesPrimary(t *testing.T) {
	testDir, feature := setupRemoveFixture(t)

	code, _, stderr := runWrk(t, feature,
		"--storage", storagePath(testDir), "remove", testDir, "--yes")
	if code == 0 {
		t.Fatalf("remove primary should not exit 0; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "primary") {
		t.Errorf("stderr should mention 'primary', got: %q", stderr)
	}
}

// TestMainRemoveRefusesNonTTYWithoutYes pins the safety gate: a
// non-terminal stdin (which `runWrk` always provides — no pty) with
// no --yes must refuse rather than silently prompt for input nobody
// will type. Exit is non-zero.
func TestMainRemoveRefusesNonTTYWithoutYes(t *testing.T) {
	testDir, feature := setupRemoveFixture(t)

	code, stdout, stderr := runWrk(t, testDir,
		"--storage", storagePath(testDir), "remove", feature)
	if code == 0 {
		t.Errorf("remove without --yes on non-TTY should not exit 0\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	}
}
