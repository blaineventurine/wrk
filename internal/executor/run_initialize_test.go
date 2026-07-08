package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
	"github.com/blaineventurine/wrk/internal/planner"
)

// initializeAction is a small builder that wires an
// InitializeResource action for direct runInitialize tests. Callers
// pass the temp directory used as ctx.Root/cwd plus the commands.
func initializeAction(root string, commands ...config.Command) planner.InitializeResource {
	return planner.InitializeResource{
		Description: "test-init",
		Context: placeholders.Context{
			Root: root,
			// Shared is unused by runInitialize itself; withLock is what
			// gates on it. Set for parity with real plans.
			Shared: filepath.Join(root, "shared"),
		},
		Commands: commands,
	}
}

// TestRunInitializeExecutesEachCommand pins the "every command runs"
// contract: given N commands, all N side effects are observable
// afterwards. A refactor that stopped early on the first success (or
// only ran the first) would leave a marker missing.
func TestRunInitializeExecutesEachCommand(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")

	action := initializeAction(
		root,
		config.Command{Run: "sh -c 'touch " + first + "'", Cwd: root},
		config.Command{Run: "sh -c 'touch " + second + "'", Cwd: root},
	)

	if err := runInitialize(action); err != nil {
		t.Fatalf("runInitialize: %v", err)
	}

	if _, err := os.Stat(first); err != nil {
		t.Errorf("first command's marker missing: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("second command's marker missing: %v", err)
	}
}

// TestRunInitializeStopsOnFailure pins the fail-fast contract: when
// command N fails, command N+1 does not run. If runInitialize
// swallowed the error or continued past it, the later marker would
// exist.
func TestRunInitializeStopsOnFailure(t *testing.T) {
	root := t.TempDir()
	shouldNotExist := filepath.Join(root, "should-not-exist")

	action := initializeAction(
		root,
		// `false` exits 1. Any real hook returning non-zero looks like
		// this to the executor.
		config.Command{Run: "false", Cwd: root},
		config.Command{Run: "sh -c 'touch " + shouldNotExist + "'", Cwd: root},
	)

	err := runInitialize(action)
	if err == nil {
		t.Fatal("expected runInitialize to surface the failing hook, got nil")
	}
	if !strings.Contains(err.Error(), "hook command failed") {
		t.Errorf("expected wrapped 'hook command failed', got %v", err)
	}
	if _, statErr := os.Stat(shouldNotExist); !os.IsNotExist(statErr) {
		t.Errorf("second command ran after first failed: err=%v", statErr)
	}
}

// TestRunInitializeAppliesCwd pins that cmd.Dir is set from the
// resolved command's Cwd: `sh -c 'touch marker'` writes into the
// declared Cwd, not into the test process's cwd. A regression that
// dropped cmd.Dir would land the marker in the wrong place (or
// PWD-dependent) and this test would fail.
func TestRunInitializeAppliesCwd(t *testing.T) {
	root := t.TempDir()

	// Cwd points into a nested dir; only there should the relative
	// `touch marker` land. Use a relative filename to isolate the Cwd
	// effect from placeholder expansion.
	cwd := filepath.Join(root, "sub")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	action := initializeAction(
		root,
		config.Command{Run: "sh -c 'touch marker'", Cwd: cwd},
	)

	if err := runInitialize(action); err != nil {
		t.Fatalf("runInitialize: %v", err)
	}

	if _, err := os.Stat(filepath.Join(cwd, "marker")); err != nil {
		t.Errorf("marker not created in Cwd=%s: err=%v", cwd, err)
	}
	// And crucially not in root (parent of the sub-dir).
	if _, err := os.Stat(filepath.Join(root, "marker")); err == nil {
		t.Errorf("marker leaked into parent %s — Cwd not honored", root)
	}
}

// TestRunInitializeAppliesEnv pins that command-level env vars reach
// the hook process. A refactor that ignored command.Env would leave
// $WRK_TEST_TOKEN empty and the captured file empty.
func TestRunInitializeAppliesEnv(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, "captured")

	action := initializeAction(
		root,
		config.Command{
			Run: "sh -c 'printf %s \"$WRK_TEST_TOKEN\" > " + out + "'",
			Cwd: root,
			Env: map[string]string{"WRK_TEST_TOKEN": "propagated"},
		},
	)

	if err := runInitialize(action); err != nil {
		t.Fatalf("runInitialize: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading captured env output: %v", err)
	}
	if string(got) != "propagated" {
		t.Errorf("captured $WRK_TEST_TOKEN = %q, want %q", got, "propagated")
	}
}

// TestRunInitializeInvalidCommandStringReturnsError pins the
// commands.Resolve error path: a Run string that shlex cannot parse
// (unterminated quote) surfaces as "resolving hook commands: ..."
// rather than a bare exec error, and no command is executed.
func TestRunInitializeInvalidCommandStringReturnsError(t *testing.T) {
	root := t.TempDir()

	action := initializeAction(
		root,
		// Unterminated single quote — shlex.Split returns an error.
		config.Command{Run: "sh -c 'unterminated", Cwd: root},
	)

	err := runInitialize(action)
	if err == nil {
		t.Fatal("expected runInitialize to reject unparseable Run string")
	}
	if !strings.Contains(err.Error(), "resolving hook commands") {
		t.Errorf("expected 'resolving hook commands' prefix, got %v", err)
	}
}

// TestRunInitializeEmptyRunReturnsError pins the "no args" tripwire:
// a resolved command with zero args (Run="") is refused before exec
// with a distinct error rather than a raw exec.Cmd failure. Planner
// validation should prevent this upstream, but the executor's
// defensive check is the last line of defense — a regression that
// dropped it would blow up further down with a less useful message.
func TestRunInitializeEmptyRunReturnsError(t *testing.T) {
	root := t.TempDir()

	action := initializeAction(
		root,
		config.Command{Run: "", Cwd: root},
	)

	err := runInitialize(action)
	if err == nil {
		t.Fatal("expected runInitialize to reject empty Run, got nil")
	}
	if !strings.Contains(err.Error(), "no arguments") {
		t.Errorf("expected 'no arguments' in error, got %v", err)
	}
}
