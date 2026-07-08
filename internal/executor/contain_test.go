package executor

import (
	"os"
	"path/filepath"
	"testing"
)

// canonRoot resolves symlinks along the temp-dir path so tests match the
// canonical-Root convention the executor relies on. On macOS
// t.TempDir() returns /var/folders/... which is itself a symlink to
// /private/var/folders/... — containedIn resolves symlinks in path, so
// comparing against a non-canonical root would spuriously look "outside".
func canonRoot(t *testing.T, root string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", root, err)
	}
	return resolved
}

func TestContainedInPathInsideRoot(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	inside := filepath.Join(root, "sub", "file")

	ok, err := containedIn(inside, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if !ok {
		t.Fatalf("expected %s to be contained in %s", inside, root)
	}
}

func TestContainedInNonExistentPathUnderRoot(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	// Deep, entirely-fictional path — every ancestor after root also
	// missing. containedIn must accept as long as some ancestor exists
	// and is inside root.
	fictional := filepath.Join(root, "nope", "still-nope", "file")

	ok, err := containedIn(fictional, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if !ok {
		t.Fatalf("expected non-existent %s to be contained in %s", fictional, root)
	}
}

func TestContainedInSymlinkEscapesRoot(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	// Symlink `<root>/escape` -> outside/target
	link := filepath.Join(root, "escape")
	target := filepath.Join(outside, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// The link itself resolves outside root.
	inside := filepath.Join(link, "file")

	ok, err := containedIn(inside, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if ok {
		t.Fatalf("expected %s to escape root %s (link -> %s)", inside, root, target)
	}
}

func TestContainedInSymlinkStaysInsideRoot(t *testing.T) {
	root := canonRoot(t, t.TempDir())

	// Symlink inside root pointing to another location inside root.
	target := filepath.Join(root, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	inside := filepath.Join(link, "file")

	ok, err := containedIn(inside, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if !ok {
		t.Fatalf("expected %s (via in-root symlink) to be contained in root %s", inside, root)
	}
}

func TestContainedInSiblingPath(t *testing.T) {
	parent := canonRoot(t, t.TempDir())

	root := filepath.Join(parent, "root")
	sibling := filepath.Join(parent, "sibling", "file")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	ok, err := containedIn(sibling, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if ok {
		t.Fatalf("expected sibling %s to be outside root %s", sibling, root)
	}
}
