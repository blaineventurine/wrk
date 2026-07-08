package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestFingerprintChangeCreatesNewVariantDir pins the load-bearing
// caching contract: a change to a fingerprint input MUST steer the
// next Link at a NEW shared subdirectory, keyed by the new digest.
// If a regression made the fingerprint stable in the face of input
// changes, the second Link would reuse the v1 subdir and silently
// serve the wrong lockfile-baked artifacts — exactly the correctness
// bug fingerprints exist to prevent.
//
// Assertions pin (a) both variant subdirs coexist under the shared
// resource dir, (b) the workspace symlink now points at v2, and
// (c) reading through the symlink yields the v2 payload.
func TestFingerprintChangeCreatesNewVariantDir(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)

	// --- v1 --------------------------------------------------------
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg-v1", "marker"), "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link v1: %v", err)
	}

	wsPath := filepath.Join(repo.Root, "node_modules")
	v1Target, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink v1: %v", err)
	}

	// --- v2: mutate fingerprint input and reseed workspace ----------
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)
	// Replace the workspace symlink with a fresh directory carrying
	// v2 payload. os.Remove on the symlink removes only the link.
	if err := os.Remove(wsPath); err != nil {
		t.Fatalf("remove workspace symlink: %v", err)
	}
	writeFile(t, filepath.Join(wsPath, "pkg-v2", "marker"), "v2\n")

	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link v2: %v", err)
	}

	// --- Assertions -----------------------------------------------
	sharedResourceRoot := filepath.Join(storage, repo.RepositoryID, "node_modules")
	entries, err := os.ReadDir(sharedResourceRoot)
	if err != nil {
		t.Fatalf("read shared resource root: %v", err)
	}

	// Exactly two variant subdirectories exist — no cleanup, no
	// clobber, no unexpected extras from a partial run.
	variantDirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			variantDirs = append(variantDirs, e.Name())
		}
	}
	if len(variantDirs) != 2 {
		t.Fatalf("variant subdirs = %v, want exactly 2", variantDirs)
	}

	v2Target, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink v2: %v", err)
	}
	if v2Target == v1Target {
		t.Fatalf("v2 symlink target unchanged after fingerprint change: %q", v2Target)
	}

	// v2 shared subdir MUST be exactly the one the workspace symlink
	// now points at.
	wantV2Dir := filepath.Dir(v2Target)
	if wantV2Dir != sharedResourceRoot {
		t.Errorf("v2 symlink parent = %q, want %q", wantV2Dir, sharedResourceRoot)
	}

	// v1 shared subdir MUST still be readable — additive caching, no
	// pruning. The check uses the exact bytes so a silent overwrite
	// with v2 payload also fails.
	v1Marker, err := os.ReadFile(filepath.Join(v1Target, "pkg-v1", "marker"))
	if err != nil {
		t.Fatalf("read v1 marker after v2 Link: %v", err)
	}
	if string(v1Marker) != "v1\n" {
		t.Errorf("v1 marker after v2 Link = %q, want %q", v1Marker, "v1\n")
	}

	// Reading through the workspace symlink shows the v2 payload.
	throughV2, err := os.ReadFile(filepath.Join(wsPath, "pkg-v2", "marker"))
	if err != nil {
		t.Fatalf("read v2 marker through symlink: %v", err)
	}
	if string(throughV2) != "v2\n" {
		t.Errorf("v2 marker through symlink = %q, want %q", throughV2, "v2\n")
	}
	// And crucially NOT the v1 payload — if the second Link had
	// pointed at the wrong subdir, `pkg-v1/marker` would still resolve.
	if _, err := os.Stat(filepath.Join(wsPath, "pkg-v1")); !os.IsNotExist(err) {
		t.Errorf("v1 payload leaked into v2 symlink target: err=%v", err)
	}
}

// TestFingerprintUnchangedReusesSameVariant pins the read side of
// the cache: repeated Links against the same fingerprint input MUST
// resolve to the SAME shared directory. Uses os.SameFile on the
// target directory so a rename or copy that preserved bytes but
// produced a new inode would still be caught.
func TestFingerprintUnchangedReusesSameVariant(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg", "marker"), "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}
	wsPath := filepath.Join(repo.Root, "node_modules")
	targetBefore, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink #1: %v", err)
	}
	statBefore, err := os.Stat(wsPath)
	if err != nil {
		t.Fatalf("stat #1: %v", err)
	}

	// Second Link — nothing changed. Must be a pure no-op at the
	// filesystem level.
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #2: %v", err)
	}
	targetAfter, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink #2: %v", err)
	}
	if targetAfter != targetBefore {
		t.Errorf("symlink target drifted across identical Links: before=%q after=%q",
			targetBefore, targetAfter)
	}
	statAfter, err := os.Stat(wsPath)
	if err != nil {
		t.Fatalf("stat #2: %v", err)
	}
	// Same inode/device — the executor did not rebuild the shared
	// directory. SameFile catches the case where a rebuild happened
	// to write the same target string but a new underlying object.
	if !os.SameFile(statBefore, statAfter) {
		t.Errorf("workspace path resolves to a different underlying directory on the second Link")
	}

	// Only ONE variant subdir exists — a spurious cache miss would
	// create a second one even if the workspace symlink still pointed
	// at the first.
	sharedResourceRoot := filepath.Join(storage, repo.RepositoryID, "node_modules")
	entries, err := os.ReadDir(sharedResourceRoot)
	if err != nil {
		t.Fatalf("readdir shared root: %v", err)
	}
	variantDirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			variantDirs = append(variantDirs, e.Name())
		}
	}
	if len(variantDirs) != 1 {
		t.Errorf("variant subdirs after two identical Links = %v, want exactly 1", variantDirs)
	}
}

// TestFingerprintVariantSwitchDoesNotClobberOtherVariants pins the
// caching-across-branches invariant: switching to a new fingerprint
// MUST leave every prior variant readable and byte-identical. This is
// what lets a user hop between two feature branches with different
// lockfiles without a `wrk link` on branch B nuking the cached
// artifacts branch A left behind.
func TestFingerprintVariantSwitchDoesNotClobberOtherVariants(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)

	// --- v1 --------------------------------------------------------
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg-v1", "marker"), "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link v1: %v", err)
	}
	wsPath := filepath.Join(repo.Root, "node_modules")
	v1Target, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink v1: %v", err)
	}

	// Snapshot v1's shared bytes so post-switch equality is checked
	// against the pre-switch state, not against a value we assume.
	v1Marker := filepath.Join(v1Target, "pkg-v1", "marker")
	v1BytesBefore, err := os.ReadFile(v1Marker)
	if err != nil {
		t.Fatalf("read v1 marker before switch: %v", err)
	}
	v1StatBefore, err := os.Stat(v1Marker)
	if err != nil {
		t.Fatalf("stat v1 marker before switch: %v", err)
	}

	// --- v2 --------------------------------------------------------
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)
	if err := os.Remove(wsPath); err != nil {
		t.Fatalf("remove workspace symlink: %v", err)
	}
	writeFile(t, filepath.Join(wsPath, "pkg-v2", "marker"), "v2\n")

	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link v2: %v", err)
	}

	// --- Assertions -----------------------------------------------
	// v1's marker still exists and holds the ORIGINAL bytes.
	v1BytesAfter, err := os.ReadFile(v1Marker)
	if err != nil {
		t.Fatalf("read v1 marker after v2 Link: %v", err)
	}
	if !bytes.Equal(v1BytesAfter, v1BytesBefore) {
		t.Errorf("v1 marker bytes changed by v2 Link: before=%q after=%q",
			v1BytesBefore, v1BytesAfter)
	}
	// And the same file (inode), so a full rebuild that recreated the
	// bytes at a new inode is caught too.
	v1StatAfter, err := os.Stat(v1Marker)
	if err != nil {
		t.Fatalf("stat v1 marker after v2 Link: %v", err)
	}
	if !os.SameFile(v1StatBefore, v1StatAfter) {
		t.Errorf("v1 marker replaced by v2 Link (SameFile mismatch)")
	}

	// And nothing v2-ish leaked into the v1 tree — a rebuild that
	// pointed both variants at the same underlying dir would satisfy
	// the byte-equality check above but drop a pkg-v2 sibling here.
	if _, err := os.Stat(filepath.Join(v1Target, "pkg-v2")); !os.IsNotExist(err) {
		t.Errorf("v2 payload leaked into v1 variant dir: err=%v", err)
	}
}
