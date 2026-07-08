package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDetectCanonicalizesRootThroughSymlink pins B4: Repository.Root
// must be symlink-resolved at detection time so downstream comparisons
// (workspace nesting, current-workspace `*` highlight) work on macOS,
// where /var, /tmp and /var/folders/... are symlinks under /private.
func TestDetectCanonicalizesRootThroughSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Real repo in a temp dir (which itself may already sit under
	// /private/var/... on macOS — we canonicalize once up-front so
	// the comparison isn't confounded).
	realRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", realRoot, "init").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// A symlink pointing at the real root, in a sibling temp dir so
	// removal is handled by t.TempDir().
	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "link")
	if err := os.Symlink(realRoot, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	repo, err := Detect(linkPath, Auto)
	if err != nil {
		t.Fatalf("Detect via symlink: %v", err)
	}

	// EvalSymlinks(linkPath) is the ground-truth canonical form.
	want, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != want {
		t.Fatalf("Repository.Root = %q, want canonical %q", repo.Root, want)
	}
}

// TestDetectCanonicalizesRootFromNestedPath makes sure detection
// canonicalizes even when the caller passes a path deep inside the
// worktree (the common case: the user's cwd).
func TestDetectCanonicalizesRootFromNestedPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	realRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", realRoot, "init").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "link")
	if err := os.Symlink(realRoot, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	nested := filepath.Join(linkPath, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	repo, err := Detect(nested, Auto)
	if err != nil {
		t.Fatalf("Detect via nested symlinked path: %v", err)
	}

	want, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != want {
		t.Fatalf("Repository.Root = %q, want canonical %q", repo.Root, want)
	}
}
