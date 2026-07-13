package executor

import (
	"errors"
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

// TestRunInitializeForceSwapsExistingVariant pins the swap-aside
// contract for the Force path: given a pre-existing variant at
// `real`, runInitialize atomically replaces its contents with the
// hook's output. The old marker file must vanish, the new one must
// appear, and neither the `.wrk-provisioning` scratch nor the
// `.wrk-deleting` swap-aside may survive the successful run.
//
// Without Force the executor's outer double-check would refuse to
// touch a pre-existing `real`, so this exercises the swap path
// directly on runInitialize where the check is bypassed.
func TestRunInitializeForceSwapsExistingVariant(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")

	// Seed the "already provisioned" state with an identifying marker.
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "old-marker"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	action := planner.InitializeResource{
		Description: "test-force",
		Context: placeholders.Context{
			Root:   root,
			Shared: shared,
		},
		Commands: []config.Command{
			{
				Run: "sh -c 'mkdir -p {shared} && touch {shared}/new-marker'",
				Cwd: root,
			},
		},
		Force: true,
	}

	if err := runInitialize(action); err != nil {
		t.Fatalf("runInitialize: %v", err)
	}

	// New marker present, old marker gone — the swap was atomic.
	if _, err := os.Stat(filepath.Join(shared, "new-marker")); err != nil {
		t.Errorf("new-marker missing after Force run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shared, "old-marker")); !os.IsNotExist(err) {
		t.Errorf("old-marker survived Force run: err=%v", err)
	}

	// Neither scratch nor swap-aside siblings may linger — a
	// successful run cleans both.
	if _, err := os.Lstat(shared + ".wrk-provisioning"); !os.IsNotExist(err) {
		t.Errorf("provisioning scratch survived: err=%v", err)
	}
	if _, err := os.Lstat(shared + ".wrk-deleting"); !os.IsNotExist(err) {
		t.Errorf("deleting sibling survived: err=%v", err)
	}
}

// TestRunInitializeForceHookFailureLeavesOldVariantIntact pins the
// rollback contract: when the Force hook exits non-zero after `real`
// has been established, the pre-existing variant must remain intact
// (its contents unchanged) and the surfaced error must name the
// hook failure. The invariant is that the caller sees either the new
// state (swap completed) OR the old state (swap rolled back), never a
// half-populated `real`.
//
// Failure BEFORE the swap-aside is the important case: no `.wrk-deleting`
// sibling has been created yet, so no rollback is needed and `real`
// stays exactly as seeded.
func TestRunInitializeForceHookFailureLeavesOldVariantIntact(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")

	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "old-marker"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	action := planner.InitializeResource{
		Description: "test-force-fail",
		Context: placeholders.Context{
			Root:   root,
			Shared: shared,
		},
		Commands: []config.Command{
			// mkdir into scratch, then exit non-zero. runInitialize
			// cleans the scratch and never reaches the swap.
			{
				Run: "sh -c 'mkdir -p {shared} && exit 7'",
				Cwd: root,
			},
		},
		Force: true,
	}

	err := runInitialize(action)
	if err == nil {
		t.Fatal("expected hook failure error, got nil")
	}
	if !strings.Contains(err.Error(), "hook command failed") {
		t.Errorf("expected 'hook command failed', got %v", err)
	}

	// The pre-existing variant must remain intact.
	got, readErr := os.ReadFile(filepath.Join(shared, "old-marker"))
	if readErr != nil {
		t.Fatalf("old-marker vanished after failed Force run: %v", readErr)
	}
	if string(got) != "keep" {
		t.Errorf("old-marker contents mutated: got %q, want %q", got, "keep")
	}

	// runInitialize cleans its own scratch on hook failure. The swap
	// hasn't happened, so no .wrk-deleting sibling either.
	if _, err := os.Lstat(shared + ".wrk-provisioning"); !os.IsNotExist(err) {
		t.Errorf("provisioning scratch survived hook failure: err=%v", err)
	}
	if _, err := os.Lstat(shared + ".wrk-deleting"); !os.IsNotExist(err) {
		t.Errorf("deleting sibling should not exist pre-swap: err=%v", err)
	}
}

// TestRunInitializeForceEmptyHookLeavesRealIntact pins the "hook
// produced nothing" edge case for the Force path: a hook that runs
// successfully but writes nothing into {shared} MUST NOT wipe the
// pre-existing variant. The atomic invariant is stronger than
// naïvely "always swap"; if the hook makes no scratch to install,
// the old variant is safer than an empty one.
func TestRunInitializeForceEmptyHookLeavesRealIntact(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")

	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "old-marker"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	action := planner.InitializeResource{
		Description: "test-force-empty",
		Context: placeholders.Context{
			Root:   root,
			Shared: shared,
		},
		Commands: []config.Command{
			// `true` succeeds without touching {shared}.
			{Run: "true", Cwd: root},
		},
		Force: true,
	}

	if err := runInitialize(action); err != nil {
		t.Fatalf("runInitialize: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(shared, "old-marker"))
	if err != nil {
		t.Fatalf("old-marker vanished after empty Force run: %v", err)
	}
	if string(got) != "keep" {
		t.Errorf("old-marker contents changed: got %q, want %q", got, "keep")
	}
	if _, err := os.Lstat(shared + ".wrk-deleting"); !os.IsNotExist(err) {
		t.Errorf("deleting sibling should not exist: err=%v", err)
	}
}

// TestExecuteInitializeResourceForceReplacesExistingVariant pins the
// full Execute path with Force=true: a plan whose sole action carries
// Force=true against a shared path that already exists MUST re-run
// the hook (the outer double-check is bypassed) and swap the variant
// contents in place.
//
// Contrast with TestRunInitializePreExistingSharedNotDisturbed, which
// pins the exact same setup with Force=false: the hook is skipped
// and the peer's content is preserved.
func TestExecuteInitializeResourceForceReplacesExistingVariant(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")

	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "peer-content"), []byte("peer-wrote-this"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Action: planner.InitializeResource{
					Description: "test-force-execute",
					Context: placeholders.Context{
						Root:   root,
						Shared: shared,
					},
					Commands: []config.Command{
						{
							Run: "sh -c 'mkdir -p {shared} && touch {shared}/hook-ran'",
							Cwd: root,
						},
					},
					Force: true,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(filepath.Join(shared, "hook-ran")); err != nil {
		t.Errorf("hook did not re-run under Force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shared, "peer-content")); !os.IsNotExist(err) {
		t.Errorf("peer-content survived Force: err=%v", err)
	}
}

// TestRunInitializeHookFailureReturnsHookError pins the typed-error
// contract runInitialize gives its callers: a failing hook surfaces
// via *HookError so the engine layer's errors.As can route the
// failure to engine.ErrHookCommandFailed under `wrk <cmd> --json`.
// Untyped `fmt.Errorf` here would fall back to the "unknown" code and
// break the machine-readable envelope.
//
// Contract pinned:
//   - errors.As extracts *HookError.
//   - Command carries the resolved command line (`false` in this case).
//   - Cwd carries the configured Cwd verbatim so operators can locate
//     the failing hook by directory.
//   - Err is a non-nil child exit error and Unwrap surfaces it so
//     errors.Is against exec-layer sentinels still works.
//   - Error() preserves the human-facing prefix "hook command failed"
//     so pre-typed log-scraping tests continue to pass.
func TestRunInitializeHookFailureReturnsHookError(t *testing.T) {
	root := t.TempDir()

	action := initializeAction(root,
		config.Command{Run: "false", Cwd: root},
	)

	err := runInitialize(action)
	if err == nil {
		t.Fatal("expected runInitialize to surface the failing hook, got nil")
	}

	var hookErr *HookError
	if !errors.As(err, &hookErr) {
		t.Fatalf("errors.As should recover *HookError, got %T: %v", err, err)
	}
	if hookErr.Command != "false" {
		t.Errorf("Command = %q, want %q", hookErr.Command, "false")
	}
	if hookErr.Cwd != root {
		t.Errorf("Cwd = %q, want %q", hookErr.Cwd, root)
	}
	if hookErr.Err == nil {
		t.Error("Err is nil; want the wrapped child exit error")
	}
	// Unwrap keeps the child error reachable so callers that keep
	// their own sentinel on the exec side stay reachable via
	// errors.Is.
	if got := errors.Unwrap(hookErr); got == nil {
		t.Error("Unwrap returned nil; want the wrapped child exit error")
	}
	// Human-facing text: pre-typed message prefix preserved so
	// grep-on-message log-scraping tests do not break.
	if !strings.Contains(hookErr.Error(), "hook command failed") {
		t.Errorf("Error() = %q, missing legacy prefix", hookErr.Error())
	}
}
