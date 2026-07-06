package executor

import (
	"os"
	"path/filepath"
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
