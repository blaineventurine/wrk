package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCountVariantsFingerprinted(t *testing.T) {
	subtree := t.TempDir()

	// Two fingerprint dirs + one bookkeeping artifact that must be ignored.
	for _, name := range []string{"abc123", "def456"} {
		if err := os.Mkdir(filepath.Join(subtree, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(subtree, "abc123.wrk-tmp"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := countVariants(subtree, true); got != 2 {
		t.Fatalf("countVariants = %d, want 2", got)
	}
}

func TestCountVariantsNonFingerprinted(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, ".env")

	if got := countVariants(shared, false); got != 0 {
		t.Fatalf("countVariants (missing) = %d, want 0", got)
	}

	if err := os.WriteFile(shared, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := countVariants(shared, false); got != 1 {
		t.Fatalf("countVariants (present) = %d, want 1", got)
	}
}

func TestTreeSize(t *testing.T) {
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b"), []byte("678"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := treeSize(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != 8 {
		t.Fatalf("treeSize = %d, want 8", got)
	}
}

func TestTreeSizeMissingIsZero(t *testing.T) {
	got, err := treeSize(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("treeSize (missing) = %d, want 0", got)
	}
}
