package executor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveAllProgressCountsRegularFileBytes pins the primary
// contract: the callback fires once per regular file with the
// pre-removal Size, and the sum matches the tree's total on-disk
// bytes. Also asserts the tree is fully gone afterwards.
func TestRemoveAllProgressCountsRegularFileBytes(t *testing.T) {
	root := t.TempDir()

	// Fixed byte counts so the sum assertion is exact: 10 + 20 + 5 = 35.
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b.txt"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "c.txt"), make([]byte, 20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d", "e.txt"), make([]byte, 5), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		total int64
		calls int
	)
	if err := RemoveAllProgress(root, func(n int64) {
		total += n
		calls++
	}); err != nil {
		t.Fatalf("RemoveAllProgress: %v", err)
	}

	if total != 35 {
		t.Errorf("total = %d, want 35", total)
	}
	if calls != 3 {
		t.Errorf("callback calls = %d, want 3 (one per regular file)", calls)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("root should be gone, got %v", err)
	}
}

// TestRemoveAllProgressMissingPathIsNoError mirrors os.RemoveAll's
// idempotence contract: a missing target is not an error. Executor
// callers rely on this for retries after a partial crash.
func TestRemoveAllProgressMissingPathIsNoError(t *testing.T) {
	// Nested-missing path so both the leaf-Lstat AND intermediate-
	// component-not-found branches route through the IsNotExist
	// tolerant path.
	if err := RemoveAllProgress(filepath.Join(t.TempDir(), "never", "there"), nil); err != nil {
		t.Errorf("missing path: %v", err)
	}
}

// TestRemoveAllProgressSymlinkNotFollowed guards the safety-critical
// property: a symlink inside root pointing OUTSIDE root MUST NOT
// cause the walk to descend through it and delete the target. This
// is exactly the semantic that lets `wrk remove` be safe against a
// malicious or accidental in-workspace symlink.
func TestRemoveAllProgressSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	keepFile := filepath.Join(outside, "keep")
	if err := os.WriteFile(keepFile, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(root, "link-to-outside")); err != nil {
		t.Fatal(err)
	}

	if err := RemoveAllProgress(root, nil); err != nil {
		t.Fatal(err)
	}

	// The keepFile outside must survive.
	if _, err := os.Stat(keepFile); err != nil {
		t.Errorf("keepFile should survive: %v", err)
	}
	// The root and its symlink are gone.
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("root should be gone, got %v", err)
	}
}

// TestRemoveAllProgressSymlinkNotCounted pins that a symlink does
// not add bytes to the counter — it is pointer-sized and following it
// would over-count anyway. Matches du's default byte-count semantics.
func TestRemoveAllProgressSymlinkNotCounted(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	var total int64
	if err := RemoveAllProgress(root, func(n int64) { total += n }); err != nil {
		t.Fatal(err)
	}
	// Only the 100-byte regular file counts. The symlink's stat
	// size (typically the target path length) MUST NOT be added.
	if total != 100 {
		t.Errorf("total = %d, want 100 (symlink size excluded)", total)
	}
}

// TestRemoveAllProgressNilCallbackSafe pins the nil-safety contract:
// callers that pass a zero-value Options.Progress MUST be able to
// invoke this without a panic.
func TestRemoveAllProgressNilCallbackSafe(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAllProgress(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("root should be gone, got %v", err)
	}
}

// TestRemoveAllProgressSingleFile: the leaf branch also must invoke
// the callback and remove the file. Not just directory trees.
func TestRemoveAllProgressSingleFile(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "f")
	if err := os.WriteFile(f, make([]byte, 42), 0o644); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := RemoveAllProgress(f, func(n int64) { total += n }); err != nil {
		t.Fatal(err)
	}
	if total != 42 {
		t.Errorf("total = %d, want 42", total)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Errorf("file should be gone, got %v", err)
	}
}
