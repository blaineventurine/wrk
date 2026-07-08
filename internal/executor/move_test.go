package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
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
