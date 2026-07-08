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

// TestTreeSizeContinuesOnPermissionError pins M11: a subdirectory
// wrk cannot read must not fail the whole walk. treeSize returns a
// lower bound (counting the readable siblings) rather than aborting
// the caller's `wrk list --size` command with a permission error.
func TestTreeSizeContinuesOnPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions; skipping")
	}

	root := t.TempDir()

	// One readable sibling with known size…
	if err := os.WriteFile(filepath.Join(root, "readable"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// …and one subtree the walk cannot enter.
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "hidden"), []byte("xxxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	// t.TempDir() cleanup will chmod the parent when tearing down; we
	// still need to restore this one so removal succeeds.
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got, err := treeSize(root)
	if err != nil {
		t.Fatalf("treeSize returned error despite permission-denied subtree: %v", err)
	}
	if got < 5 {
		t.Fatalf("treeSize = %d, want at least 5 (readable sibling)", got)
	}
}
