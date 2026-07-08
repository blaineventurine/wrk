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

// TestContainedInEmptyInputs pins the fast-return: an empty path or
// empty root MUST be treated as "not contained" without walking the
// filesystem. A regression that fell through to filepath.Abs("")
// would try to canonicalize the process CWD, which is almost never
// what the caller intended.
func TestContainedInEmptyInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		root string
	}{
		{name: "empty path", path: "", root: "/tmp"},
		{name: "empty root", path: "/tmp/file", root: ""},
		{name: "both empty", path: "", root: ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ok, err := containedIn(tc.path, tc.root)
			if err != nil {
				t.Errorf("containedIn(%q, %q) err = %v, want nil", tc.path, tc.root, err)
			}
			if ok {
				t.Errorf("containedIn(%q, %q) = true, want false", tc.path, tc.root)
			}
		})
	}
}

// TestContainedInLeafSymlinkPointsOutsideRoot pins the load-bearing
// property that Detach and Symlink actions replace their own leaf
// symlink — a symlink AT the leaf position must NOT be dereferenced
// during containment, or every Detach on a fully-linked workspace
// would false-positive against a symlink already pointing into shared
// storage (which is by design outside the workspace root).
//
// Regression: before this fix, `wrk detach` with the default
// out-of-repo storage failed with "escapes workspace root" because
// canonicalize resolved the workspace-side symlink's target.
func TestContainedInLeafSymlinkPointsOutsideRoot(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	sharedStorage := canonRoot(t, t.TempDir())

	// Simulate a linked resource: `<root>/.env` -> `<sharedStorage>/.env`
	target := filepath.Join(sharedStorage, ".env")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	ok, err := containedIn(link, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if !ok {
		t.Fatalf("expected leaf-symlink %s (pointing to %s) to be contained in root %s; "+
			"Detach must be able to operate on its own workspace-side link",
			link, target, root)
	}
}

// TestContainedInAncestorSymlinkEscapesEvenWhenLeafExists pins the
// counterpart: an ancestor-level symlink escape must still be caught
// even when the leaf itself happens to exist inside the linked-to
// target. The old canonicalize resolved everything and caught this;
// the new leaf-preserving canonicalize must still catch it via the
// ancestor walk.
func TestContainedInAncestorSymlinkEscapesEvenWhenLeafExists(t *testing.T) {
	root := canonRoot(t, t.TempDir())
	outside := canonRoot(t, t.TempDir())

	// Ancestor symlink: `<root>/tools` -> `<outside>/tools`
	target := filepath.Join(outside, "tools")
	if err := os.MkdirAll(filepath.Join(target, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "tools")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// Leaf `build` exists inside the escaped target — this is the C4
	// scenario. Must be rejected because the ANCESTOR `tools` escapes.
	inside := filepath.Join(link, "build")

	ok, err := containedIn(inside, root)
	if err != nil {
		t.Fatalf("containedIn: %v", err)
	}
	if ok {
		t.Fatalf("expected %s to escape root via ancestor symlink %s -> %s",
			inside, link, target)
	}
}
