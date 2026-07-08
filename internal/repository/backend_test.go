package repository

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestCaptureStripsGitEnvVars pins S5: capture must never leak the
// caller's git-directory overrides into the child. If wrk is invoked
// from a git hook, GIT_DIR/GIT_WORK_TREE point at the hook's
// repository, and passing them through would splice foreign state
// into wrk's decisions.
func TestCaptureStripsGitEnvVars(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	// Set the whole documented set, plus one representative from each
	// of the other stripped keys, so a partial strip fails loudly.
	overrides := map[string]string{
		"GIT_DIR":                          "/nowhere/git",
		"GIT_WORK_TREE":                    "/nowhere/work",
		"GIT_COMMON_DIR":                   "/nowhere/common",
		"GIT_INDEX_FILE":                   "/nowhere/index",
		"GIT_OBJECT_DIRECTORY":             "/nowhere/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": "/nowhere/alt",
	}
	for k, v := range overrides {
		t.Setenv(k, v)
	}

	// Probe each key; a stripped variable expands to "unset".
	script := ""
	for k := range overrides {
		script += "echo " + k + "=${" + k + ":-unset};"
	}

	out, err := capture(t.TempDir(), "sh", "-c", script)
	if err != nil {
		t.Fatalf("capture sh: %v", err)
	}

	for k := range overrides {
		want := k + "=unset"
		if !strings.Contains(out, want) {
			t.Errorf("capture leaked %s into child; output:\n%s", k, out)
		}
	}
}

// TestCaptureForcesCLocale complements TestCaptureStripsGitEnvVars:
// the sanitized env sets LC_ALL=C so wrk's parsers see the C-locale
// versions of every VCS message.
func TestCaptureForcesCLocale(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	t.Setenv("LANG", "fr_FR.UTF-8")

	out, err := capture(t.TempDir(), "sh", "-c", "echo LC_ALL=$LC_ALL LANG=$LANG")
	if err != nil {
		t.Fatalf("capture sh: %v", err)
	}
	if !strings.Contains(out, "LC_ALL=C") || !strings.Contains(out, "LANG=C") {
		t.Fatalf("capture did not force C locale; output: %q", out)
	}
}

// TestWrapExecErrorPreservesExitError pins M4: wrapExecError must
// keep the *exec.ExitError chain intact so callers can errors.As
// through it. Formatting an exit error with %s throws that identity
// away, silently breaking every caller that switches on exit code.
func TestWrapExecErrorPreservesExitError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	_, err := capture(t.TempDir(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("capture: expected error from failing subprocess")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("errors.As did not unwrap to *exec.ExitError; got: %v", err)
	}
	if code := exitErr.ExitCode(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	// stderr should be spliced into the wrapped message for humans.
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("wrapped message does not mention stderr: %v", err)
	}
}

// TestWrapExecErrorPreservesErrNotFound is the second half of M4:
// exec.ErrNotFound propagates through the wrap so callers can
// distinguish "the binary isn't installed" from "the binary ran and
// failed".
func TestWrapExecErrorPreservesErrNotFound(t *testing.T) {
	_, err := capture(
		t.TempDir(),
		"wrk-definitely-not-a-real-binary-xyz",
	)
	if err == nil {
		t.Fatal("capture: expected error from missing binary")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("errors.Is did not find exec.ErrNotFound; got: %v", err)
	}
}
