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
		cmd := exec.Command("go", "build", "-o", out, "./cmd/wrk")
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
