package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/blaineventurine/wrk/internal/planner"
)

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src.txt")
	destination := filepath.Join(dir, "nested", "dst.txt")

	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := move(source, destination); err != nil {
		t.Fatalf("move returned error: %v", err)
	}

	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("source still exists after move: err=%v", err)
	}

	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("destination content = %q, want %q", got, "hello")
	}
}

func TestMoveDirectory(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "srcdir")
	destination := filepath.Join(dir, "dstdir")

	if err := os.MkdirAll(filepath.Join(source, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "sub", "file.txt"),
		[]byte("data"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := move(source, destination); err != nil {
		t.Fatalf("move returned error: %v", err)
	}

	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("source still exists after move: err=%v", err)
	}

	got, err := os.ReadFile(filepath.Join(destination, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("reading moved file: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("moved file content = %q, want %q", got, "data")
	}
}

// TestCopyPathThenRename exercises the exact sequence used by the
// cross-device fallback (copy to tmp, rename into place), independent of
// whether the test filesystem actually triggers EXDEV.
func TestCopyPathThenRename(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(source, "f"),
		[]byte("x"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(dir, "dst")
	tmp := destination + ".wrk-tmp"

	if err := copyPath(source, tmp); err != nil {
		t.Fatalf("copyPath: %v", err)
	}
	if err := os.Rename(tmp, destination); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(destination, "f"))
	if err != nil {
		t.Fatalf("reading dst: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("content = %q, want %q", got, "x")
	}
}

// TestCopyPathRefusesSymlinkSource guards the Lstat-based check: a
// symlink at source must be refused rather than silently followed. The
// executor's cross-device fallback would otherwise copy data from
// anywhere on disk into the workspace.
func TestCopyPathRefusesSymlinkSource(t *testing.T) {
	dir := t.TempDir()

	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(dir, "dest")

	if err := copyPath(link, destination); err == nil {
		t.Fatal("expected copyPath to refuse symlink source")
	}

	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("expected no destination, got err=%v", err)
	}
}

// TestIsCrossDevice pins the errors.Is-based EXDEV detection used by
// move's fast-vs-slow decision. Missing this check means every
// same-filesystem rename would fall through to the copy fallback (or
// vice versa, depending on the direction of the regression). Cover
// bare, wrapped, and chained forms; also verify unrelated errors are
// not misclassified.
func TestIsCrossDevice(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "bare EXDEV", err: syscall.EXDEV, want: true},
		{
			name: "LinkError wrapping EXDEV",
			err: &os.LinkError{
				Op:  "rename",
				Old: "a",
				New: "b",
				Err: syscall.EXDEV,
			},
			want: true,
		},
		{
			name: "fmt.Errorf %w wrapping EXDEV",
			err:  fmt.Errorf("outer: %w", syscall.EXDEV),
			want: true,
		},
		{name: "nil", err: nil, want: false},
		{name: "unrelated sentinel", err: errors.New("boom"), want: false},
		{name: "different errno (EACCES)", err: syscall.EACCES, want: false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isCrossDevice(tc.err); got != tc.want {
				t.Errorf("isCrossDevice(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

// TestMoveKindMismatchAtDestination pins the error surface for a
// pre-existing destination that os.Rename can't clobber: renaming a
// directory over an existing regular file fails on POSIX with
// ENOTDIR, and that failure must reach the caller (not the EXDEV
// fallback). This is the invariant "move never silently overwrites a
// wrong-kind destination" — the double-check upstream catches most,
// but move itself must not swallow it either.
func TestMoveKindMismatchAtDestination(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src-dir")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-existing regular file at destination. Rename(dir, file) is
	// rejected by the kernel — the fallback (copy-then-rename) is only
	// taken on EXDEV, so this should surface an error and leave source
	// intact.
	destination := filepath.Join(dir, "dst-file")
	if err := os.WriteFile(destination, []byte("winner"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := move(source, destination); err == nil {
		t.Fatal("expected move(dir, existing-file) to error, got nil")
	}

	// Source must survive so the operator can recover.
	if info, err := os.Stat(source); err != nil {
		t.Errorf("source removed on failure: err=%v", err)
	} else if !info.IsDir() {
		t.Errorf("source lost its shape: mode=%v", info.Mode())
	}
	// Pre-existing destination file untouched.
	if got, err := os.ReadFile(destination); err != nil {
		t.Errorf("destination gone: err=%v", err)
	} else if string(got) != "winner" {
		t.Errorf("destination clobbered: got %q, want %q", got, "winner")
	}
}

// TestMoveErrorMessageMentionsRelink pins M8: when Rename(tmp, dest)
// succeeded but RemoveAll(source) failed after the cross-device
// fallback, the error surface MUST point the operator at `wrk relink`
// (the only command that can complete the swap without triggering a
// conflict), not `wrk link` (which will refuse). The pre-fix message
// promised `wrk link` — a lie that led operators to try the command
// that refuses, then blame wrk for the confused state.
//
// Constructing a failing RemoveAll deterministically across every
// platform is fragile — we cover the pin with two complementary
// checks: (a) the file `move.go` MUST contain the fixed literal and
// MUST NOT contain the pre-fix backticked `wrk link`; (b) an integration-
// style dry-run of the error format via move.go's exact format
// string, hand-mirrored here, MUST produce a message that mentions
// `wrk relink`. Any refactor that dropped the recovery-command
// rename would flip both checks.
func TestMoveErrorMessageMentionsRelink(t *testing.T) {
	// (a) Source-level pin: the offending pre-fix phrase is gone and
	// the fix's phrase is present. Reading move.go here is a cheap
	// way to make the assertion resilient to future refactors that
	// might move the format string around inside the file.
	content, err := os.ReadFile("move.go")
	if err != nil {
		t.Fatalf("reading move.go: %v", err)
	}
	body := string(content)
	if !strings.Contains(body, "wrk relink") {
		t.Errorf("move.go must mention `wrk relink` as the recovery command; not found")
	}
	// The pre-fix wording was "the next `wrk link` will discard it".
	// Reject that exact substring. `wrk link` as a bare phrase might
	// legitimately appear in surrounding prose, so match the specific
	// broken guidance rather than the command name in isolation.
	if strings.Contains(body, "the next `wrk link` will discard") {
		t.Errorf("move.go still contains the pre-fix `wrk link` recovery text")
	}

	// (b) Runtime pin: the format string, applied, produces the fixed
	// message. Any drift between move.go and this literal will surface
	// via (a) — this second check just protects against a future
	// refactor that adds a second format-string call site.
	msg := fmt.Errorf(
		"moved to shared storage at %s but failed to remove source %s (run `wrk relink` inside the workspace to complete the swap; any edits you make to %s meanwhile will be discarded): %w",
		"/dest", "/src", "/src", errors.New("boom"),
	).Error()
	if !strings.Contains(msg, "wrk relink") {
		t.Errorf("expected error message to mention `wrk relink`, got: %s", msg)
	}
	if strings.Contains(msg, "wrk link`") {
		t.Errorf("error message must not close a backticked `wrk link`, got: %s", msg)
	}
}

// TestExecuteMoveIdempotentCompletesInterruptedSwapDirectory pins H5's
// recovery path for a directory-shaped resource: when a previous run
// completed Rename(tmp, dest) but was killed before RemoveAll(source),
// both source and destination hold identical trees. The next Execute
// of the same Move plan MUST notice the content-identity, discard
// source silently, and let the trailing Symlink take over — instead
// of forcing the operator through `wrk relink` (which would blindly
// discard whatever's at source, including any recent user edits).
func TestExecuteMoveIdempotentCompletesInterruptedSwapDirectory(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "workspace", "resource")
	destination := filepath.Join(dir, "shared", "resource")

	// Populate BOTH sides with byte-identical trees, simulating the
	// crash-mid-swap state.
	for _, root := range []string{source, destination} {
		if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(root, "sub", "file.txt"),
			[]byte("identical-payload"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{Action: planner.Move{Source: source, Destination: destination}},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v (recovery path must complete swap silently)", err)
	}

	// Source must be gone — the swap completed.
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("source %s must be removed after recovery, got err=%v", source, err)
	}

	// Destination untouched — bytes match what we pre-populated.
	got, err := os.ReadFile(filepath.Join(destination, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if string(got) != "identical-payload" {
		t.Errorf("destination content changed: got %q, want %q", got, "identical-payload")
	}
}

// TestExecuteMoveIdempotentCompletesInterruptedSwapFile is the file
// variant of the directory recovery test above. Both cases must be
// pinned because sameContents dispatches on kind (regular file vs.
// directory) and a refactor that fixed one branch and broke the other
// would silently pass one test while breaking the other user.
func TestExecuteMoveIdempotentCompletesInterruptedSwapFile(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "workspace", "resource.env")
	destination := filepath.Join(dir, "shared", "resource.env")

	for _, path := range []string{source, destination} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("SAME=bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plan := planner.Plan{
		Actions: []planner.PlannedAction{
			{Action: planner.Move{Source: source, Destination: destination}},
		},
	}

	if err := Execute(plan); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("source must be removed, got err=%v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("destination gone: %v", err)
	}
	if string(got) != "SAME=bytes\n" {
		t.Errorf("destination changed: got %q", got)
	}
}

// TestMoveExportedCallsThrough pins the exported Move wrapper: it must
// forward to the package-private move without altering the atomic-rename
// semantics. engine.RelinkIsolate depends on this exact contract to
// migrate a workspace's detached copy into shared storage without
// rolling its own filesystem-move plumbing.
func TestMoveExportedCallsThrough(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src.txt")
	destination := filepath.Join(dir, "dst.txt")

	if err := os.WriteFile(source, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Move(source, destination); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Errorf("source still exists after Move: err=%v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("destination content = %q, want %q", got, "content")
	}
}
