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

// TestBackendForKnown pins the VCS→backend dispatch: Git yields
// gitBackend, JJ yields jjBackend. Swapping the two branches would
// silently route every git repo through jj (or vice versa) and every
// downstream call would fail with a confusing "no such command"
// error at the shell layer.
func TestBackendForKnown(t *testing.T) {
	cases := []struct {
		name string
		vcs  VCS
		want backend
	}{
		{"git", Git, gitBackend{}},
		{"jj", JJ, jjBackend{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backendFor(tc.vcs)
			if err != nil {
				t.Fatalf("backendFor(%q): %v", tc.vcs, err)
			}
			// Compare by kind() rather than concrete type equality
			// — it is the observable contract every caller uses.
			if got.kind() != tc.want.kind() {
				t.Fatalf("backendFor(%q).kind() = %q, want %q",
					tc.vcs, got.kind(), tc.want.kind())
			}
		})
	}
}

// TestBackendForAutoErrors pins that Auto — the sentinel meaning
// "detect at runtime" — MUST NOT reach backendFor. detectVCS resolves
// Auto into a concrete VCS first; if a refactor ever lets Auto through,
// callers would get a nil backend and panic later at the first
// operation. Failing fast at the switch is the whole point.
func TestBackendForAutoErrors(t *testing.T) {
	b, err := backendFor(Auto)
	if err == nil {
		t.Fatal("backendFor(Auto) returned nil error; want unsupported")
	}
	if b != nil {
		t.Fatalf("backendFor(Auto) returned non-nil backend %v; want nil", b)
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("backendFor(Auto) error = %v, want mention of unsupported", err)
	}
	// The VCS string is quoted in the error so users can tell what
	// they asked for. An empty quote would be a regression.
	if !strings.Contains(err.Error(), string(Auto)) {
		t.Fatalf("backendFor(Auto) error = %v, want mention of %q",
			err, Auto)
	}
}

// TestBackendForUnknownErrors pins the default arm: any string that
// isn't Git or JJ (typo, forged VCS constant) yields an error rather
// than a nil backend the caller would deref.
func TestBackendForUnknownErrors(t *testing.T) {
	b, err := backendFor(VCS("hg"))
	if err == nil {
		t.Fatal("backendFor(unknown) returned nil error")
	}
	if b != nil {
		t.Fatalf("backendFor(unknown) returned non-nil backend %v", b)
	}
	if !strings.Contains(err.Error(), `"hg"`) {
		t.Fatalf("error should quote the bad VCS; got: %v", err)
	}
}

// TestPassthroughSuccessReturnsNil pins the happy path: a command that
// exits 0 must produce no error. `sh -c 'exit 0'` is the smallest
// deterministic zero-exit we can run cross-platform on POSIX CI.
func TestPassthroughSuccessReturnsNil(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	if err := passthrough(t.TempDir(), "sh", "-c", "exit 0"); err != nil {
		t.Fatalf("passthrough sh exit 0: got err = %v, want nil", err)
	}
}

// TestPassthroughNonZeroExitReturnsError pins the failure path: a
// non-zero exit MUST surface as an error, and that error MUST wrap the
// underlying *exec.ExitError so callers can errors.As through it.
// Losing the exit-error identity would break every downstream
// switch-on-exit-code the harness runs.
func TestPassthroughNonZeroExitReturnsError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	err := passthrough(t.TempDir(), "sh", "-c", "exit 7")
	if err == nil {
		t.Fatal("passthrough sh exit 7: got nil, want error")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("passthrough did not wrap *exec.ExitError; got %v", err)
	}
	if code := exitErr.ExitCode(); code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	// The wrap must at least name the failing command so a user
	// reading the log can tell which invocation went wrong.
	if !strings.Contains(err.Error(), "sh") {
		t.Fatalf("passthrough error missing command name: %v", err)
	}
}

// TestPassthroughMissingBinaryReturnsErrNotFound pins the "binary not
// on PATH" branch: the wrap MUST preserve exec.ErrNotFound so callers
// can distinguish "not installed" from "ran and failed".
func TestPassthroughMissingBinaryReturnsErrNotFound(t *testing.T) {
	err := passthrough(
		t.TempDir(),
		"wrk-definitely-not-a-real-binary-xyz",
		"anything",
	)
	if err == nil {
		t.Fatal("passthrough: expected error from missing binary")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("errors.Is did not find exec.ErrNotFound; got: %v", err)
	}
}
