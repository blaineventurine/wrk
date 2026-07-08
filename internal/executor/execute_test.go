package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
