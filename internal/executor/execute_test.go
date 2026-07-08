package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/resolver"
)

func TestExecuteEmptyPlan(t *testing.T) {
	if err := Execute(planner.Plan{}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteCreateDirectory(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(root, "a", "b", "c")

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.CreateDirectory{
					Path: path,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestExecuteRefusesSymlinkEscape guards the runtime containment check.
// A Symlink action whose Link falls outside plan.WorkspaceRoot (via an
// in-root symlink pointing outside) must be refused before the executor
// creates or mutates anything.
func TestExecuteRefusesSymlinkEscape(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	// Symlink `<root>/escape` -> outside — anything under `escape/` is
	// really under `outside/`.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "escape", "resource")
	target := filepath.Join(t.TempDir(), "shared-resource")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Symlink{
					Link:   link,
					Target: target,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to refuse symlink escape, got nil error")
	}

	// Nothing must have been created at the escaping link.
	if _, statErr := os.Lstat(link); !os.IsNotExist(statErr) {
		t.Errorf("expected no symlink at %s, got err=%v", link, statErr)
	}
}

// TestExecuteMoveDoubleCheckSameKindDiscardsSource covers S2's happy
// path: when the winning racer already placed a directory of the same
// kind at the destination, our workspace source is safely discarded.
func TestExecuteMoveDoubleCheckSameKindDiscardsSource(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "workspace", "resource")
	destination := filepath.Join(dir, "shared", "resource")

	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "marker"), []byte("workspace"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-existing destination directory — same kind, different marker
	// to prove that Execute does not overwrite it.
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "marker"), []byte("winner"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Move{
					Source:      source,
					Destination: destination,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("expected workspace source to be discarded, Lstat err=%v", err)
	}

	// Destination content must be untouched (the winner's data wins).
	got, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil {
		t.Fatalf("reading destination marker: %v", err)
	}
	if string(got) != "winner" {
		t.Errorf("destination marker = %q, want %q (workspace clobbered the winner)", got, "winner")
	}
}

// TestExecuteMoveDoubleCheckKindMismatchRefuses covers S2's guard: if
// something at the destination is not the same kind as the workspace
// source, we refuse and leave the source untouched. A regular file
// where a directory belongs is exactly the "hand the user a link to
// garbage" scenario the fix exists to prevent.
func TestExecuteMoveDoubleCheckKindMismatchRefuses(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "workspace", "resource")
	destination := filepath.Join(dir, "shared", "resource")

	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "marker"), []byte("workspace"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	// Regular file where the executor expects a directory.
	if err := os.WriteFile(destination, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Move{
					Source:      source,
					Destination: destination,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to refuse kind mismatch, got nil error")
	}
	if !strings.Contains(err.Error(), "not the expected kind") {
		t.Errorf("expected error to mention kind mismatch, got %v", err)
	}

	// Workspace source must survive: the user needs it to recover.
	if info, err := os.Stat(source); err != nil {
		t.Errorf("workspace source removed on refusal: err=%v", err)
	} else if !info.IsDir() {
		t.Errorf("workspace source lost its shape: mode=%v", info.Mode())
	}
}

// TestExecuteMoveDoubleCheckRejectsSymlinkDestination guards the
// "shared storage should be real bytes, not indirection" clause of S2.
// Even if the workspace source happens to be missing (or is itself
// weird), a symlink at the destination must not qualify as a "winner
// already provisioned it" signal.
func TestExecuteMoveDoubleCheckRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "workspace", "resource")
	destination := filepath.Join(dir, "shared", "resource")

	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	// Symlink pointing somewhere plausible — even if the target exists
	// and is a directory of the right kind, indirection at the shared
	// path is disallowed.
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, destination); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Move{
					Source:      source,
					Destination: destination,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to refuse symlink destination, got nil error")
	}
	if !strings.Contains(err.Error(), "not the expected kind") {
		t.Errorf("expected error to mention kind mismatch, got %v", err)
	}
}

// TestExecuteSymlinkRefusesRealDirectory covers D5: an existing real
// directory at the Link path yields the new refusal error, not a bare
// "file exists" from the trailing Symlink call. The directory itself
// must be left intact so operators can investigate.
func TestExecuteSymlinkRefusesRealDirectory(t *testing.T) {
	dir := t.TempDir()

	link := filepath.Join(dir, "node_modules")
	target := filepath.Join(dir, "shared", "node_modules")

	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(link, "marker"), []byte("keepme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Symlink{
					Link:   link,
					Target: target,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to refuse replacing a real directory, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to replace") {
		t.Errorf("expected clear refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected error to name the kind found (directory), got %v", err)
	}

	// The real directory and its content must survive.
	if info, err := os.Lstat(link); err != nil {
		t.Errorf("real directory removed on refusal: err=%v", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Errorf("expected real directory to survive, got mode=%v", info.Mode())
	}
	got, err := os.ReadFile(filepath.Join(link, "marker"))
	if err != nil {
		t.Fatalf("reading marker after refusal: %v", err)
	}
	if string(got) != "keepme" {
		t.Errorf("marker content = %q, want %q", got, "keepme")
	}
}

// TestExecuteSymlinkReplacesExistingSymlink covers D5's happy path:
// when Link is already a symlink (broken or valid, any target), the
// executor replaces it with the new link. This is the common case
// after a shared destination changes.
func TestExecuteSymlinkReplacesExistingSymlink(t *testing.T) {
	dir := t.TempDir()

	link := filepath.Join(dir, "node_modules")
	oldTarget := filepath.Join(dir, "old")
	newTarget := filepath.Join(dir, "new")

	if err := os.MkdirAll(oldTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldTarget, link); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Symlink{
					Link:   link,
					Target: newTarget,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != newTarget {
		t.Errorf("link target = %q, want %q", got, newTarget)
	}
}

// TestExecuteSymlinkReplacesBrokenSymlink is the paranoid variant of
// the above: a symlink whose current target doesn't exist must still
// be safely replaced. Prior to D5 this exercised the "file exists"
// bug indirectly (Remove worked fine, os.Symlink then failed with
// EEXIST if Remove was silently ignored).
func TestExecuteSymlinkReplacesBrokenSymlink(t *testing.T) {
	dir := t.TempDir()

	link := filepath.Join(dir, "node_modules")
	dead := filepath.Join(dir, "does-not-exist")
	newTarget := filepath.Join(dir, "new")

	if err := os.MkdirAll(newTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dead, link); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Symlink{
					Link:   link,
					Target: newTarget,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != newTarget {
		t.Errorf("link target = %q, want %q", got, newTarget)
	}
}

// TestExecuteSymlinkRefusesRealFile is the file-kind variant of
// TestExecuteSymlinkRefusesRealDirectory — a regular file at Link
// must also be refused rather than silently deleted.
func TestExecuteSymlinkRefusesRealFile(t *testing.T) {
	dir := t.TempDir()

	link := filepath.Join(dir, "config.json")
	target := filepath.Join(dir, "shared", "config.json")

	if err := os.WriteFile(link, []byte("keepme"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action: planner.Symlink{
					Link:   link,
					Target: target,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to refuse replacing a real file, got nil error")
	}
	if !strings.Contains(err.Error(), "refusing to replace") {
		t.Errorf("expected clear refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("expected error to name the kind found (regular file), got %v", err)
	}

	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("reading link after refusal: %v", err)
	}
	if string(got) != "keepme" {
		t.Errorf("expected real file preserved, got %q", got)
	}
}

// TestEnvironmentIsSorted pins M18: the KEY=VALUE slice environment
// returns must be sorted by key. os/exec doesn't care about the order,
// but hook logs (`wrk --debug` etc.) become reproducible only if the
// environment we assemble is stable across runs; Go's map iteration is
// not.
func TestEnvironmentIsSorted(t *testing.T) {
	// Keys chosen so map-order flakes would surface quickly: they all
	// start with distinct letters and lexicographic order does not
	// match any obvious insertion pattern.
	in := map[string]string{
		"zeta":   "z",
		"alpha":  "a",
		"middle": "m",
	}

	got := environment(in)
	want := []string{"alpha=a", "middle=m", "zeta=z"}

	if len(got) != len(want) {
		t.Fatalf("environment(...) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("environment[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestEnvironmentEmpty pins the fast path: an empty input returns nil
// so callers can hand the result straight to exec.Cmd.Env, which
// treats nil as "inherit".
func TestEnvironmentEmpty(t *testing.T) {
	if got := environment(nil); got != nil {
		t.Fatalf("environment(nil) = %v, want nil", got)
	}
	if got := environment(map[string]string{}); got != nil {
		t.Fatalf("environment(empty) = %v, want nil", got)
	}
}

// TestExecuteCreateDirectoryMode pins the mode CreateDirectory hands to
// os.MkdirAll: shared and workspace dirs are created 0o755 so a
// world-readable shared cache is discoverable by peer users. A silent
// tightening to 0o700 would surface here.
func TestExecuteCreateDirectoryMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "created")

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Instance: resolver.ResourceInstance{},
				Action:   planner.CreateDirectory{Path: path},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got mode=%v", info.Mode())
	}
	// Compare against the process umask: MkdirAll masks the requested
	// perms by the umask. The contract is "we asked for 0o755"; the
	// observable mode is 0o755 &^ umask. A macOS/Linux default umask
	// of 0o022 leaves 0o755 unchanged.
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("CreateDirectory mode = %o, want %o", got, 0o755)
	}
}

// TestExecuteCreateDirectoryIdempotent pins the "MkdirAll on existing
// directory is not an error" contract Execute relies on when re-running
// a plan after a partial success. A refactor that switched to os.Mkdir
// would redden here.
func TestExecuteCreateDirectoryIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "already-here")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{Action: planner.CreateDirectory{Path: path}},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute (idempotent CreateDirectory): %v", err)
	}
}

// TestExecuteMoveHappyPath pins Move's default happy path through
// Execute: source and destination on the same filesystem, no
// pre-existing destination, so os.Rename takes the fast path and the
// content lands intact under the new location while the source is
// gone.
func TestExecuteMoveHappyPath(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	source := filepath.Join(root, "workspace", "resource")
	destination := filepath.Join(root, "shared", "resource")

	if err := os.MkdirAll(filepath.Join(source, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "sub", "file"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{
				Action: planner.Move{
					Source:      source,
					Destination: destination,
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("source not removed after Move: err=%v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "sub", "file"))
	if err != nil {
		t.Fatalf("reading moved file: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("moved content = %q, want %q", got, "payload")
	}
}

// TestExecuteRemoveAction pins Remove: a real file under the workspace
// root is gone after Execute. Distinct from safeRemove-refusal tests;
// this is the wire-through case.
func TestExecuteRemoveAction(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	victim := filepath.Join(root, "cache", "stale")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Remove{Path: victim}},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, got err=%v", victim, err)
	}
}

// TestExecuteRemoveActionRefusesEscape guards containment on Remove: a
// symlink-relative path that resolves outside plan.WorkspaceRoot must
// be refused and the target on the other side must survive.
func TestExecuteRemoveActionRefusesEscape(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(outside, "keep")
	if err := os.WriteFile(precious, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Remove{Path: filepath.Join(root, "escape", "keep")}},
		},
	}

	if err := Execute(plan); err == nil {
		t.Fatal("expected refusal for out-of-workspace Remove")
	}

	if _, err := os.Stat(precious); err != nil {
		t.Errorf("out-of-workspace file removed: err=%v", err)
	}
}

// TestExecuteDetachAction pins the Detach wire-through: a Link that is
// currently a symlink to Target becomes a real, independent copy of
// Target after Execute; mutations to the copy no longer affect the
// shared original.
func TestExecuteDetachAction(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	target := filepath.Join(root, "shared", "config.env")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("shared-body"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, ".env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Detach{Link: link, Target: target}},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat detached link: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected detached link to be a regular file, got mode=%v", info.Mode())
	}

	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("ReadFile detached link: %v", err)
	}
	if string(got) != "shared-body" {
		t.Errorf("detached content = %q, want %q", got, "shared-body")
	}

	// Independence: mutate the copy, shared target unchanged.
	if err := os.WriteFile(link, []byte("local-only"), 0o644); err != nil {
		t.Fatal(err)
	}
	if orig, err := os.ReadFile(target); err != nil {
		t.Fatalf("ReadFile target: %v", err)
	} else if string(orig) != "shared-body" {
		t.Errorf("shared target mutated after detach: got %q, want %q", orig, "shared-body")
	}
}

// TestExecuteDetachActionRefusesEscape guards containment on Detach.
// Detach mutates the workspace side (Link), so a Link that resolves
// outside plan.WorkspaceRoot must be refused before any file is
// touched.
func TestExecuteDetachActionRefusesEscape(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "shared", "res")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "escape", "res")

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Detach{Link: link, Target: target}},
		},
	}

	if err := Execute(plan); err == nil {
		t.Fatal("expected refusal for out-of-workspace Detach")
	}

	// Nothing created at escaping link path.
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("expected no file at escaping link, got err=%v", err)
	}
}

// initResourceMarker builds an InitializeResource action whose only
// side effect is `touch <marker>` from within workspaceRoot. The
// returned plan is complete and ready for Execute.
func initResourceMarker(t *testing.T, workspaceRoot, shared, marker string) planner.Plan {
	t.Helper()

	return planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Action: planner.InitializeResource{
					Description: "test-init",
					Context: placeholders.Context{
						Root:   workspaceRoot,
						Shared: shared,
					},
					Commands: []config.Command{
						{
							Run: "sh -c 'touch " + marker + "'",
							Cwd: workspaceRoot,
						},
					},
				},
			},
		},
	}
}

// TestExecuteInitializeResourceRunsHook pins the wire-through: an
// InitializeResource plan runs the hook's commands, and their side
// effects survive.
func TestExecuteInitializeResourceRunsHook(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	marker := filepath.Join(root, "hook-ran")

	if err := Execute(initResourceMarker(t, root, shared, marker)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("expected hook to have created %s, got err=%v", marker, err)
	}
}

// TestExecuteInitializeResourceSkippedWhenSharedExists pins the S1
// double-check: if the shared resource already exists when Execute
// acquires the lock, the hook is NOT run. This is the "peer beat us to
// it" case; running the hook again would waste work and could stomp
// on the winner's output.
func TestExecuteInitializeResourceSkippedWhenSharedExists(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	marker := filepath.Join(root, "hook-ran")

	// Pre-provision `shared` — a marker file whose mere existence
	// signals "peer finished". Executor must not overwrite it and must
	// not run the hook.
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shared, []byte("peer-wrote-this"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Execute(initResourceMarker(t, root, shared, marker)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("expected hook to be skipped, marker exists: err=%v", err)
	}
	// Peer's shared file must be untouched.
	if got, err := os.ReadFile(shared); err != nil {
		t.Fatalf("reading peer shared: %v", err)
	} else if string(got) != "peer-wrote-this" {
		t.Errorf("peer shared clobbered: got %q, want %q", got, "peer-wrote-this")
	}
}

// TestExecuteInitializeResourceCommandOrder pins the ordering
// guarantee: commands in InitializeResource.Commands run in the order
// declared, sequentially. A future refactor that ran them concurrently
// or reversed them would produce the wrong log.
func TestExecuteInitializeResourceCommandOrder(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	log := filepath.Join(root, "order.log")

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Action: planner.InitializeResource{
					Context: placeholders.Context{
						Root:   root,
						Shared: shared,
					},
					Commands: []config.Command{
						{Run: "sh -c 'printf one >> " + log + "'", Cwd: root},
						{Run: "sh -c 'printf two >> " + log + "'", Cwd: root},
						{Run: "sh -c 'printf three >> " + log + "'", Cwd: root},
					},
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if string(got) != "onetwothree" {
		t.Errorf("command order log = %q, want %q", got, "onetwothree")
	}
}

// TestExecuteInitializeResourceEnvVars pins that per-command Env
// entries reach the hook process. A refactor that dropped Env would
// still let the hook run (green!) but with the wrong environment;
// this test observes the env value through the hook's side effect.
func TestExecuteInitializeResourceEnvVars(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	out := filepath.Join(root, "env.out")

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Action: planner.InitializeResource{
					Context: placeholders.Context{
						Root:   root,
						Shared: shared,
					},
					Commands: []config.Command{
						{
							Run: "sh -c 'printf %s \"$WRK_TEST_TOKEN\" > " + out + "'",
							Cwd: root,
							Env: map[string]string{"WRK_TEST_TOKEN": "expected-value"},
						},
					},
				},
			},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading env.out: %v", err)
	}
	if string(got) != "expected-value" {
		t.Errorf("env-forwarded value = %q, want %q", got, "expected-value")
	}
}

// TestExecuteInitializeResourceSurfacesHookError pins the "hook
// failures surface" contract at the Execute layer: a hook that exits
// non-zero causes Execute to return an error that mentions the hook
// command, not a bare exec error and not nil. A regression where the
// InitializeResource branch swallowed runInitialize's error would
// silently green-light a broken workspace.
func TestExecuteInitializeResourceSurfacesHookError(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Action: planner.InitializeResource{
					Context: placeholders.Context{
						Root:   root,
						Shared: shared,
					},
					Commands: []config.Command{
						{Run: "false", Cwd: root},
					},
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to surface hook failure, got nil")
	}
	if !strings.Contains(err.Error(), "hook command failed") {
		t.Errorf("expected 'hook command failed' in error, got %v", err)
	}

	// Shared must not have been left half-provisioned.
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		t.Errorf("expected shared absent after failed init, got err=%v", err)
	}
}

// TestExecuteMoveRefusesEscapedSource pins the ensureContained gate
// on Move.Source. Even if the destination path is inside a workspace,
// a source that escapes (via an in-root symlink) must be refused
// before the executor touches the destination.
func TestExecuteMoveRefusesEscapedSource(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	// Materialize the outside "workspace" that the escape points to.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	realSource := filepath.Join(outside, "resource")
	if err := os.MkdirAll(realSource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSource, "keep"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source resolves outside the plan's WorkspaceRoot.
	escapingSource := filepath.Join(root, "escape", "resource")
	destination := filepath.Join(root, "shared", "resource")

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{
				Action: planner.Move{
					Source:      escapingSource,
					Destination: destination,
				},
			},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Move source-escape refusal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes workspace root") {
		t.Errorf("expected 'escapes workspace root' in error, got %v", err)
	}

	// Real out-of-workspace source untouched.
	if got, err := os.ReadFile(filepath.Join(realSource, "keep")); err != nil {
		t.Errorf("out-of-workspace source removed: err=%v", err)
	} else if string(got) != "safe" {
		t.Errorf("out-of-workspace source mutated: got %q, want %q", got, "safe")
	}
	// No destination materialized.
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("destination materialized despite refusal: err=%v", err)
	}
}

// TestExecuteUnknownActionType pins the default branch of the type
// switch: a PlannedAction whose Action interface is nil (or, in
// principle, any unimplemented Action type) is rejected with a
// "unknown action type" error rather than silently no-op'd. This is
// the executor's tripwire for a planner regression that emits an
// action shape the executor doesn't know how to run.
func TestExecuteUnknownActionType(t *testing.T) {
	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{Action: nil},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to reject nil action, got nil")
	}
	if !strings.Contains(err.Error(), "unknown action type") {
		t.Errorf("expected 'unknown action type' in error, got %v", err)
	}
}

// TestExecuteSymlinkMkdirParentBlocked pins the MkdirAll error path
// inside the Symlink branch: if the Link's parent chain cannot be
// materialized (a regular file blocks the way), the executor
// surfaces the MkdirAll error and does not proceed to create a
// symlink at an impossible path.
func TestExecuteSymlinkMkdirParentBlocked(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	// Place a regular file at what would need to be a directory
	// component of Link's parent path.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(blocker, "child", "link")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Symlink{Link: link, Target: target}},
		},
	}

	if err := Execute(plan); err == nil {
		t.Fatal("expected Execute to surface MkdirAll error, got nil")
	}

	// Blocker file untouched.
	if got, err := os.ReadFile(blocker); err != nil {
		t.Errorf("blocker removed on failure: err=%v", err)
	} else if string(got) != "in the way" {
		t.Errorf("blocker mutated on failure: got %q", got)
	}
}

// TestExecuteCreateDirectoryMkdirFails pins the MkdirAll error path
// for CreateDirectory: when a regular file blocks the parent chain,
// Execute surfaces the error rather than silently succeeding.
func TestExecuteCreateDirectoryMkdirFails(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{Action: planner.CreateDirectory{Path: filepath.Join(blocker, "child")}},
		},
	}

	if err := Execute(plan); err == nil {
		t.Fatal("expected MkdirAll error, got nil")
	}
}

// TestExecuteMoveDoubleCheckMissingSource pins line 47 of execute.go:
// if the destination already exists AND the workspace source has
// vanished between the plan being built and Execute running, the
// os.Lstat(source) error is surfaced rather than swallowed. A silent
// pass here would tell the caller "moved" when nothing moved.
func TestExecuteMoveDoubleCheckMissingSource(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	source := filepath.Join(root, "workspace", "resource") // does not exist
	destination := filepath.Join(root, "shared", "resource")

	// Pre-existing destination directory — triggers the double-check
	// branch which then reads source.
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	// Ancestor of source must exist so ensureContained is happy.
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Move{Source: source, Destination: destination}},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected error when double-check reads missing source, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected NotExist error, got %v", err)
	}
}

// TestExecuteRemoveSurfacesGuardRefusal pins line 98: safeRemove's
// "repository metadata" refusal must propagate through Execute. A
// plan that tried to Remove a directory containing .git would
// otherwise silently erase the user's history.
func TestExecuteRemoveSurfacesGuardRefusal(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	// A fake repository nested inside the workspace: contains a .git.
	repo := filepath.Join(root, "cache", "some-clone")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Remove{Path: repo}},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected safeRemove refusal to surface, got nil")
	}
	if !strings.Contains(err.Error(), "repository metadata") {
		t.Errorf("expected 'repository metadata' phrasing, got %v", err)
	}

	// Repo must be intact.
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Errorf("repository .git removed on refusal: err=%v", err)
	}
}

// TestExecuteDetachSurfacesMissingTarget pins line 110: a detach with
// a missing Target must surface the copyPath error, not silently
// leave the link broken. This is the wire-through for the underlying
// TestDetachMissingTargetFails.
func TestExecuteDetachSurfacesMissingTarget(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	target := filepath.Join(root, "shared", "missing")
	link := filepath.Join(root, "workspace", "link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Detach{Link: link, Target: target}},
		},
	}

	if err := Execute(plan); err == nil {
		t.Fatal("expected detach error to propagate, got nil")
	}
}

// TestExecuteSymlinkOSSymlinkFails pins line 155: when the parent
// directory of Link exists but is read-only, os.MkdirAll is a no-op
// and the trailing os.Symlink call fails. The error must reach the
// caller instead of being masked.
func TestExecuteSymlinkOSSymlinkFails(t *testing.T) {
	// Ordinary user only — root would bypass the read-only check.
	if os.Geteuid() == 0 {
		t.Skip("os.Symlink cannot fail with EACCES when running as root")
	}

	root := canonRoot(t, t.TempDir())

	parent := filepath.Join(root, "readonly")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	// Ensure teardown can remove the read-only dir.
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(parent, "link")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	plan := planner.Plan{
		WorkspaceRoot: root,
		Actions: []planner.PlannedAction{
			{Action: planner.Symlink{Link: link, Target: target}},
		},
	}

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected os.Symlink to fail against read-only parent, got nil")
	}

	// Link must NOT have been created.
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("expected no link, got err=%v", err)
	}
}

// initResourceShared builds an InitializeResource plan whose hook writes
// into `{shared}`. Callers control the tail of the shell command so a
// test can force success or failure after the mkdir. This is the
// C4-regression shape: the audit's crash scenario is exactly a hook
// that half-populates `{shared}` and then exits nonzero.
func initResourceShared(workspaceRoot, shared, tail string) planner.Plan {
	return planner.Plan{
		Actions: []planner.PlannedAction{
			{
				Action: planner.InitializeResource{
					Description: "test-init",
					Context: placeholders.Context{
						Root:   workspaceRoot,
						Shared: shared,
					},
					Commands: []config.Command{
						{
							Run: "sh -c 'mkdir -p {shared} && " + tail + "'",
							Cwd: workspaceRoot,
						},
					},
				},
			},
		},
	}
}

// TestRunInitializeHookFailureLeavesSharedMissing pins C4: a hook that
// half-populates `{shared}` and then exits non-zero MUST leave the
// filesystem in a "not provisioned" state — neither the real shared
// path nor the scratch sibling may survive. Prior to the fix, the
// hook's partial `mkdir -p` would materialize a bare directory at
// `shared`, and the outer double-check (`Stat(shared)` succeeds) would
// then permanently skip the hook on every future Link, leaving the
// workspace symlinked at broken shared.
func TestRunInitializeHookFailureLeavesSharedMissing(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := initResourceShared(root, shared, "exit 1")

	err := Execute(plan)
	if err == nil {
		t.Fatal("expected Execute to surface hook failure, got nil")
	}

	if _, err := os.Lstat(shared); !os.IsNotExist(err) {
		t.Errorf("shared %s must not exist after failing hook, got err=%v", shared, err)
	}
	// The atomic-provision scratch must also be gone; otherwise a
	// stale sibling would waste disk and confuse operators inspecting
	// the storage layout.
	scratch := shared + ".wrk-provisioning"
	if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
		t.Errorf("scratch %s must not survive failure, got err=%v", scratch, err)
	}
}

// TestRunInitializeHookRetryReRunsAfterFix pins the retry contract of
// C4: after a failed hook leaves nothing at `shared`, the operator's
// next Link with a fixed hook MUST re-run the hook (Inspect sees "not
// provisioned") and succeed. This is what the atomic-provision buys
// over the pre-fix "half a directory tricks the double-check" bug.
func TestRunInitializeHookRetryReRunsAfterFix(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	if err := os.MkdirAll(filepath.Dir(shared), 0o755); err != nil {
		t.Fatal(err)
	}

	// First attempt: hook fails mid-provisioning.
	if err := Execute(initResourceShared(root, shared, "exit 1")); err == nil {
		t.Fatal("expected first Execute to fail, got nil")
	}

	// Confirm the fix left no trace at shared.
	if _, err := os.Lstat(shared); !os.IsNotExist(err) {
		t.Fatalf("shared must be missing before retry, got err=%v", err)
	}

	// Second attempt: hook succeeds and drops a proof-of-execution
	// marker inside the shared directory.
	if err := Execute(initResourceShared(root, shared, "touch {shared}/.installed")); err != nil {
		t.Fatalf("retry Execute: %v", err)
	}

	if _, err := os.Stat(shared); err != nil {
		t.Errorf("shared %s must exist after successful retry, got err=%v", shared, err)
	}
	if _, err := os.Stat(filepath.Join(shared, ".installed")); err != nil {
		t.Errorf("expected retry hook to have created %s/.installed, got err=%v", shared, err)
	}
}

// TestRunInitializePreExistingSharedNotDisturbed pins the outer
// double-check invariant unaffected by C4: a shared resource that
// already exists (peer completed provisioning) MUST NOT be replaced
// nor trigger the hook. The atomic-provision fix must not accidentally
// clobber a peer's completed work.
func TestRunInitializePreExistingSharedNotDisturbed(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared", "resource")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(shared, "peer-content")
	if err := os.WriteFile(peer, []byte("peer-wrote-this"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A hook that would drop a distinct marker inside `{shared}` if it
	// ran. If the outer double-check does its job, the hook is skipped
	// entirely and the marker is absent.
	plan := initResourceShared(root, shared, "touch {shared}/hook-ran")

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got, err := os.ReadFile(peer); err != nil {
		t.Errorf("peer content vanished: %v", err)
	} else if string(got) != "peer-wrote-this" {
		t.Errorf("peer content clobbered: got %q, want %q", got, "peer-wrote-this")
	}
	if _, err := os.Stat(filepath.Join(shared, "hook-ran")); !os.IsNotExist(err) {
		t.Errorf("hook must have been skipped, got err=%v", err)
	}
}
