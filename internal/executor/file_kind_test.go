package executor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestFileKindOther pins the fallback branch of fileKind for a mode
// that is not a symlink, directory, or regular file. A named pipe
// (FIFO) is the most portable trigger on POSIX; verifying the
// "other" label matters because the error messages surfaced to the
// operator include it verbatim.
//
// Skipped when the temp filesystem refuses mkfifo (e.g. on some
// non-POSIX environments) — the test asserts the fileKind contract,
// not the OS's mkfifo support.
func TestFileKindOther(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")

	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}

	info, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}

	if got := fileKind(info); got != "other" {
		t.Errorf("fileKind(fifo) = %q, want %q", got, "other")
	}
}
