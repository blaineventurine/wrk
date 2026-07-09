package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitFlagsYesRegistered pins the flag wiring for `wrk init --yes`
// / `-y`. --yes is only meaningful under --force against an existing
// .wrk.yml, but the flag must exist so users can pass it without
// tripping an unknown-flag error.
func TestInitFlagsYesRegistered(t *testing.T) {
	long := initCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on initCmd")
	}
	short := initCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on initCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to initYes)")
	}
}

// TestInitFlagsForceRegistered pins that `wrk init --force` / `-f`
// is still wired.
func TestInitFlagsForceRegistered(t *testing.T) {
	long := initCmd.Flags().Lookup("force")
	if long == nil {
		t.Fatal("--force flag not registered on initCmd")
	}
	short := initCmd.Flags().ShorthandLookup("f")
	if short == nil {
		t.Fatal("-f shorthand not registered on initCmd")
	}
	if long != short {
		t.Fatal("--force and -f must be the same flag (bound to initForce)")
	}
}

// TestInitForceOnExistingRefusesWithoutYes pins the destructive-
// action gate: `wrk init --force` against a repo that already has a
// .wrk.yml MUST print the "Overwriting" preview and then refuse
// without --yes on a non-TTY. The pre-existing config bytes MUST
// survive the refusal — otherwise the prompt is a lie.
func TestInitForceOnExistingRefusesWithoutYes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")
	original := "resources: []  # deliberate marker so we can detect overwrite\n"
	writeFile(t, filepath.Join(repo, ".wrk.yml"), original)

	code, stdout, stderr := runWrk(t, repo, "init", "--force")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Overwriting") {
		t.Errorf("stdout should announce the overwrite before refusing, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr should mention --yes so users know the fix, got: %q", stderr)
	}

	// The pre-existing config MUST survive a refusal — the whole
	// point of the prompt is to protect it.
	got, err := readFile(t, filepath.Join(repo, ".wrk.yml"))
	if err != nil {
		t.Fatalf("read post-refusal .wrk.yml: %v", err)
	}
	if got != original {
		t.Fatalf("refused init still overwrote .wrk.yml:\ngot:\n%s\nwant:\n%s", got, original)
	}
}

// TestInitForceYesOverwritesSilently pins the happy path for a
// permitted overwrite: --force AND --yes together proceed without
// prompting, and the new file is written. This is what scripts and
// CI will invoke; anything less than a silent success would break
// them.
func TestInitForceYesOverwritesSilently(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")
	writeFile(t, filepath.Join(repo, ".wrk.yml"),
		"resources: []  # placeholder that init should replace\n")

	code, stdout, stderr := runWrk(t, repo, "init", "--force", "--yes")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	// The overwrite banner should still print — it's informational.
	if !strings.Contains(stdout, "Overwriting") {
		t.Errorf("stdout should announce the overwrite even when silent, got:\n%s", stdout)
	}
	// And the new file must have been written (the detection line
	// mentions the env fixture we planted).
	if !strings.Contains(stdout, "env") {
		t.Errorf("stdout should mention the 'env' detection after successful overwrite, got:\n%s", stdout)
	}

	// The .wrk.yml on disk should no longer be the placeholder.
	got, err := readFile(t, filepath.Join(repo, ".wrk.yml"))
	if err != nil {
		t.Fatalf("read post-overwrite .wrk.yml: %v", err)
	}
	if strings.Contains(got, "placeholder that init should replace") {
		t.Fatalf(".wrk.yml still contains the placeholder — overwrite did not run:\n%s", got)
	}
}

// TestInitFreshRepoNoConfirmPrompt pins that a plain `wrk init` in
// a repo WITHOUT a pre-existing .wrk.yml never asks for consent.
// There is nothing destructive about writing the file for the first
// time, so a spurious prompt (or non-TTY refusal) would block every
// first-time user unnecessarily.
func TestInitFreshRepoNoConfirmPrompt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")

	code, stdout, stderr := runWrk(t, repo, "init")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Overwriting") {
		t.Errorf("fresh init should not print 'Overwriting'; got:\n%s", stdout)
	}
	if strings.Contains(stderr, "--yes") {
		t.Errorf("fresh init should not refuse for --yes; got:\n%s", stderr)
	}
}

// readFile is a t.Helper wrapper around os.ReadFile that returns the
// content as a string — the CLI init tests compare against small
// YAML snippets that are easier to eyeball as strings than []byte.
func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
