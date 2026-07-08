package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeRemoveRefusesRoot pins the sentinel guard: safeRemove refuses
// "/" outright and its RemoveAll is never reached. This is the
// last-resort backstop against a plan that somehow proposes a Remove
// of the filesystem root.
func TestSafeRemoveRefusesRoot(t *testing.T) {
	err := safeRemove("/")
	if err == nil {
		t.Fatal("expected safeRemove(\"/\") to refuse, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to remove") {
		t.Errorf("expected 'refusing to remove' in error, got %v", err)
	}
}

// TestSafeRemoveRefusesDot pins the same guard for "." — a relative
// current-directory reference the loader may produce from a
// malformed manifest.
func TestSafeRemoveRefusesDot(t *testing.T) {
	err := safeRemove(".")
	if err == nil {
		t.Fatal("expected safeRemove(\".\") to refuse, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to remove") {
		t.Errorf("expected 'refusing to remove' in error, got %v", err)
	}
}

// TestSafeRemoveRefusesRepositoryMarkers pins the "path contains
// repository metadata" guard: a directory holding a .git or .jj
// child is treated as a repository root and refused. Removing it
// would blow away VCS history. Table-driven so both known markers are
// covered by the same shape.
func TestSafeRemoveRefusesRepositoryMarkers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker string
	}{
		{name: "git", marker: ".git"},
		{name: "jj", marker: ".jj"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			// The marker can be a directory OR a file (jj sometimes uses
			// a file). os.Stat is what safeRemove uses; either satisfies
			// it. Directory is closer to real repos.
			if err := os.Mkdir(filepath.Join(root, tc.marker), 0o755); err != nil {
				t.Fatal(err)
			}
			// Drop a payload alongside the marker so we can verify the
			// refusal was total — nothing under root is touched.
			payload := filepath.Join(root, "payload")
			if err := os.WriteFile(payload, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}

			err := safeRemove(root)
			if err == nil {
				t.Fatalf("expected safeRemove to refuse repo root %s, got nil", root)
			}
			if !strings.Contains(err.Error(), "repository metadata") {
				t.Errorf("expected 'repository metadata' phrasing, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.marker) {
				t.Errorf("expected marker %q named in error, got %v", tc.marker, err)
			}

			// Nothing under root removed.
			if _, err := os.Stat(root); err != nil {
				t.Errorf("root removed on refusal: err=%v", err)
			}
			if _, err := os.Stat(filepath.Join(root, tc.marker)); err != nil {
				t.Errorf("marker %s removed on refusal: err=%v", tc.marker, err)
			}
			if got, err := os.ReadFile(payload); err != nil {
				t.Errorf("payload removed on refusal: err=%v", err)
			} else if string(got) != "keep" {
				t.Errorf("payload mutated on refusal: got %q, want %q", got, "keep")
			}
		})
	}
}

// TestSafeRemoveDirectoryHappyPath pins the "no markers, safe to
// remove" happy path: a plain directory with content vanishes.
// safeRemove wraps os.RemoveAll, so children are gone too.
func TestSafeRemoveDirectoryHappyPath(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "cache")
	nested := filepath.Join(victim, "sub", "file")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := safeRemove(victim); err != nil {
		t.Fatalf("safeRemove: %v", err)
	}

	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, got err=%v", victim, err)
	}
}

// TestSafeRemoveFileHappyPath pins the file case: a plain regular
// file (no marker semantics apply) is removed cleanly.
func TestSafeRemoveFileHappyPath(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "file.txt")
	if err := os.WriteFile(victim, []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := safeRemove(victim); err != nil {
		t.Fatalf("safeRemove: %v", err)
	}

	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Errorf("expected file removed, got err=%v", err)
	}
}

// TestSafeRemoveMissingPathIsNoOp pins the RemoveAll fall-through: a
// nonexistent path returns nil (RemoveAll's documented behavior). The
// executor relies on this for idempotence when re-running a plan.
func TestSafeRemoveMissingPathIsNoOp(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "does", "not", "exist")

	if err := safeRemove(victim); err != nil {
		t.Errorf("expected nil for missing path, got %v", err)
	}
}
