package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// globConfig is the shared glob-resource shape: one Resources entry
// whose Path contains a `*` segment, which the resolver expands into
// as many ResourceInstances as there are filesystem matches at plan
// time. No fingerprint is configured, so each match gets its own
// per-instance shared path (RelativePath-derived).
const globConfig = "resources:\n" +
	"  - name: node\n" +
	"    path: packages/*/node_modules\n"

// seedGlobWorkspace makes exactly two matches for packages/*/node_modules
// and one non-match (packages/c has no node_modules), so the tests can
// pin (a) that BOTH matches are handled, and (b) that a sibling package
// without the glob-tail is left strictly alone.
func seedGlobWorkspace(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "packages", "a", "node_modules", "dep-a"), "payload-a\n")
	writeFile(t, filepath.Join(root, "packages", "b", "node_modules", "dep-b"), "payload-b\n")
	// packages/c exists as a bare directory: never matches the glob,
	// so nothing here should ever grow a node_modules symlink.
	if err := os.MkdirAll(filepath.Join(root, "packages", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestGlobResourceLinkExpandsToAllMatches pins that a single resource
// entry with `packages/*/node_modules` produces two independent
// symlinks — one per matching directory — pointing at DIFFERENT shared
// paths (because each match's RelativePath is different and no
// fingerprint collapses them). Also pins that a non-matching sibling
// (packages/c) is never touched. A regression that expanded to only
// the first match, or that collapsed both instances onto one shared
// path, would flip one of these three assertions.
func TestGlobResourceLinkExpandsToAllMatches(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, globConfig)
	seedGlobWorkspace(t, repo.Root)

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	aLink := filepath.Join(repo.Root, "packages", "a", "node_modules")
	bLink := filepath.Join(repo.Root, "packages", "b", "node_modules")

	assertSymlink(t, aLink)
	assertSymlink(t, bLink)

	// Non-matching sibling MUST NOT have gained a node_modules entry.
	// A broken glob that widened matches (e.g. matched every directory
	// under packages/) would create packages/c/node_modules.
	if _, err := os.Lstat(filepath.Join(repo.Root, "packages", "c", "node_modules")); !os.IsNotExist(err) {
		t.Errorf("packages/c grew a node_modules entry; err=%v (glob widened past its intended matches)", err)
	}

	aTarget, err := os.Readlink(aLink)
	if err != nil {
		t.Fatalf("readlink a: %v", err)
	}
	bTarget, err := os.Readlink(bLink)
	if err != nil {
		t.Fatalf("readlink b: %v", err)
	}
	wantA, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "packages", "a", "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	wantB, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "packages", "b", "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	if aTarget != wantA {
		t.Errorf("packages/a target = %q, want %q", aTarget, wantA)
	}
	if bTarget != wantB {
		t.Errorf("packages/b target = %q, want %q", bTarget, wantB)
	}
	// Distinctness matters — no fingerprint means no shared-collapse.
	if aTarget == bTarget {
		t.Errorf("both instances resolved to the same shared path %q; per-match RelativePath was ignored", aTarget)
	}

	// Read through each symlink and verify the correct child survived
	// the Move (a swap of the two payloads would prove the loop is
	// mis-indexed).
	if got, err := os.ReadFile(filepath.Join(aLink, "dep-a")); err != nil || string(got) != "payload-a\n" {
		t.Errorf("through packages/a: got=%q err=%v, want %q", got, err, "payload-a\n")
	}
	if got, err := os.ReadFile(filepath.Join(bLink, "dep-b")); err != nil || string(got) != "payload-b\n" {
		t.Errorf("through packages/b: got=%q err=%v, want %q", got, err, "payload-b\n")
	}
}

// TestGlobResourceDetachHandlesAllInstances pins that Detach, like
// Link, walks every glob-expanded instance — both matches become real
// directories, and BOTH workspace-relative paths land in the detach
// registry. A regression that recorded only the first-match instance
// would flip the registry assertion; one that missed detaching either
// instance would flip the filesystem assertion.
func TestGlobResourceDetachHandlesAllInstances(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, globConfig)
	seedGlobWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Both symlinks are now real dirs with the original payloads back
	// on the workspace side.
	assertRealDirWithFile(t, filepath.Join(repo.Root, "packages", "a", "node_modules"), "dep-a", "payload-a\n")
	assertRealDirWithFile(t, filepath.Join(repo.Root, "packages", "b", "node_modules"), "dep-b", "payload-b\n")

	// Registry MUST hold both workspace-relative paths (slash-form,
	// per detachedPaths()'s ToSlash normalization). filepath.Glob
	// doesn't guarantee match order across platforms, so sort before
	// comparing.
	got := sortedRegistryEntry(readRegistry(t, repo), repo.Root)
	want := []string{"packages/a/node_modules", "packages/b/node_modules"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("registry = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("registry[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestGlobResourceRelinkAllInstances pins the Relink destructive
// contract across the full glob fan-out: after Link -> Detach ->
// user mutates the workspace copies -> Relink, BOTH instances are
// re-symlinked and BOTH sets of local edits are discarded. A regression
// that reconciled only the first instance would leave the second
// instance's edits on disk and its symlink absent.
func TestGlobResourceRelinkAllInstances(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, globConfig)
	seedGlobWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// User edits both workspace copies — Relink must discard both.
	aChild := filepath.Join(repo.Root, "packages", "a", "node_modules", "dep-a")
	bChild := filepath.Join(repo.Root, "packages", "b", "node_modules", "dep-b")
	writeFile(t, aChild, "edited-a\n")
	writeFile(t, bChild, "edited-b\n")

	if err := Relink(repo, opts); err != nil {
		t.Fatalf("Relink: %v", err)
	}

	// Both back to symlinks with original shared payloads visible
	// through them.
	assertSymlink(t, filepath.Join(repo.Root, "packages", "a", "node_modules"))
	assertSymlink(t, filepath.Join(repo.Root, "packages", "b", "node_modules"))
	if got, err := os.ReadFile(aChild); err != nil || string(got) != "payload-a\n" {
		t.Errorf("post-Relink packages/a: got=%q err=%v, want %q (edits should be discarded)", got, err, "payload-a\n")
	}
	if got, err := os.ReadFile(bChild); err != nil || string(got) != "payload-b\n" {
		t.Errorf("post-Relink packages/b: got=%q err=%v, want %q (edits should be discarded)", got, err, "payload-b\n")
	}
}

// assertSymlink is a shared assertion for glob-resource tests: the path
// exists AND is a symlink. Fails with the observed mode so a
// regression that leaves a real file/dir is diagnosable at a glance.
func assertSymlink(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is not a symlink; mode=%v", path, info.Mode())
	}
}

// assertRealDirWithFile is a shared assertion: path is a real
// directory (never a symlink) and holds `child` with `content` exactly.
func assertRealDirWithFile(t *testing.T, path, child, content string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s still a symlink; mode=%v", path, info.Mode())
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory; mode=%v", path, info.Mode())
	}
	got, err := os.ReadFile(filepath.Join(path, child))
	if err != nil {
		t.Fatalf("read %s/%s: %v", path, child, err)
	}
	if string(got) != content {
		t.Errorf("%s/%s = %q, want %q", path, child, got, content)
	}
}
