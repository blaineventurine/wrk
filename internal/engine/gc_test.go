package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gofrs/flock"
)

// TestScanVariantsEmptyStorage: fresh repo, no wrk link ever run.
// scanVariants returns no variants (not an error).
func TestScanVariantsEmptyStorage(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	variants, err := scanVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("scanVariants: %v", err)
	}
	if len(variants) != 0 {
		t.Fatalf("expected 0 variants on empty storage, got %d", len(variants))
	}
}

// TestScanVariantsUnFingerprintedResource: a single .env resource with
// no fingerprint. After Link, scanVariants finds exactly one variant
// with empty Fingerprint pointing at the shared file's parent path.
func TestScanVariantsUnFingerprintedResource(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	variants, err := scanVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("scanVariants: %v", err)
	}
	if len(variants) != 1 {
		t.Fatalf("expected 1 variant, got %d: %+v", len(variants), variants)
	}
	v := variants[0]
	if v.Resource != "env" {
		t.Errorf("Resource = %q, want env", v.Resource)
	}
	if v.Path != ".env" {
		t.Errorf("Path = %q, want .env", v.Path)
	}
	if v.Fingerprint != "" {
		t.Errorf("Fingerprint = %q, want empty (un-fingerprinted)", v.Fingerprint)
	}
	if v.StoragePath == "" {
		t.Error("StoragePath must be set")
	}
	if v.Size == 0 {
		t.Errorf("Size = 0, want >0 for a provisioned resource")
	}
	if v.LastUsed.IsZero() {
		t.Error("LastUsed should be populated from Stat().ModTime()")
	}
}

// TestScanVariantsFingerprintedTwoVariants: provision two node_modules
// variants by rewriting package.json between two Link runs. Confirm
// scanVariants returns both, each with a distinct fingerprint.
func TestScanVariantsFingerprintedTwoVariants(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\" && touch \"{shared}/.installed\"'\n",
		"package.json": `{"version":"1"}`,
	})
	storage := storageIn(t, repo.Root)

	// Variant 1.
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link #1: %v", err)
	}
	// Rewrite the fingerprint input so the next Link builds a new variant.
	writeFile(t, filepath.Join(repo.Root, "package.json"), `{"version":"2"}`)
	// Remove the previous symlink so Link doesn't see it as "already correctly linked".
	_ = os.Remove(filepath.Join(repo.Root, "node_modules"))
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link #2: %v", err)
	}

	variants, err := scanVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("scanVariants: %v", err)
	}
	if len(variants) != 2 {
		var names []string
		for _, v := range variants {
			names = append(names, v.Fingerprint)
		}
		sort.Strings(names)
		t.Fatalf("expected 2 variants, got %d: %v", len(variants), names)
	}
	for _, v := range variants {
		if v.Resource != "node" {
			t.Errorf("Resource = %q, want node", v.Resource)
		}
		if v.Fingerprint == "" {
			t.Errorf("variant missing Fingerprint: %+v", v)
		}
		if v.StoragePath == "" {
			t.Errorf("variant missing StoragePath: %+v", v)
		}
	}
}

// TestScanVariantsIgnoresBookkeeping: seed a .wrk-lock file alongside a
// variant. scanVariants must skip it.
func TestScanVariantsIgnoresBookkeeping(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\"'\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Sprinkle bookkeeping siblings that must all be filtered.
	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	for _, name := range []string{
		"5fd1d0d610ba6c17.wrk-lock",
		"stray.wrk-deleting",
		"stray.wrk-forgetting",
		"orphan.wrk-tmp",
	} {
		writeFile(t, filepath.Join(resourceDir, name), "junk")
	}

	variants, err := scanVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("scanVariants: %v", err)
	}
	if len(variants) != 1 {
		t.Fatalf("expected 1 variant (bookkeeping filtered), got %d", len(variants))
	}
}

// TestPinnedVariantsSinglePinsItsVariant: primary workspace's Link'd
// variant is pinned; no stray variants are reported.
func TestPinnedVariantsSinglePinsItsVariant(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: env\n" +
			"    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	pinned, unreachable, err := pinnedVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("pinnedVariants: %v", err)
	}
	if len(unreachable) != 0 {
		t.Errorf("unreachable = %v, want empty", unreachable)
	}
	if len(pinned) != 1 {
		t.Fatalf("pinned = %v, want exactly 1", pinned)
	}
}

// TestPinnedVariantsTwoWorkspacesTwoVariants: after re-linking to a
// second fingerprint the primary points at v2. Two variants are on
// disk, but only the currently-linked one is pinned.
func TestPinnedVariantsTwoWorkspacesTwoVariants(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\"'\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, primary.Root)

	if err := Link(primary, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("primary Link v1: %v", err)
	}

	// Re-fingerprint by rewriting package.json and re-linking. This
	// mints a second variant on disk while the workspace symlink now
	// points to it.
	writeFile(t, filepath.Join(primary.Root, "package.json"), `{"v":2}`)
	_ = os.Remove(filepath.Join(primary.Root, "node_modules"))
	if err := Link(primary, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("primary Link v2: %v", err)
	}

	variants, err := scanVariants(primary, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("scanVariants: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("expected 2 variants on disk, got %d", len(variants))
	}

	pinned, _, err := pinnedVariants(primary, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("pinnedVariants: %v", err)
	}
	if len(pinned) != 1 {
		t.Fatalf("pinned = %v, want exactly 1 (only the currently-linked variant)", pinned)
	}
}

// TestPinnedVariantsDetachedWorkspaceDoesNotPin: after Detach converts
// the managed symlink into a real dir, the previous variant is no
// longer pinned by this workspace.
func TestPinnedVariantsDetachedWorkspaceDoesNotPin(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: env\n" +
			"    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	pinned, _, err := pinnedVariants(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("pinnedVariants: %v", err)
	}
	if len(pinned) != 0 {
		t.Fatalf("pinned = %v, want empty after detach", pinned)
	}
}

// TestCleanBookkeepingDetectFindsOrphanedLock: a lock file without any
// sibling variant subdir must be flagged for sweep.
func TestCleanBookkeepingDetectFindsOrphanedLock(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	// Seed a lock file with no corresponding variant.
	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(resourceDir, "5fd1d0d6.wrk-lock")
	writeFile(t, orphan, "")

	result, err := cleanBookkeepingDetect(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(result.OrphanedLocks) != 1 || result.OrphanedLocks[0] != orphan {
		t.Fatalf("OrphanedLocks = %v, want [%q]", result.OrphanedLocks, orphan)
	}
}

// TestCleanBookkeepingDetectSkipsHeldLock: a .wrk-provisioning whose
// sibling .wrk-lock is currently held by another process must NOT be
// reported — someone is actively provisioning it.
func TestCleanBookkeepingDetectSkipsHeldLock(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prov := filepath.Join(resourceDir, "5fd1d0d6.wrk-provisioning")
	if err := os.MkdirAll(prov, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(resourceDir, "5fd1d0d6.wrk-lock")
	writeFile(t, lockPath, "")

	// Hold the lock during the test.
	l := flock.New(lockPath)
	ok, err := l.TryLock()
	if err != nil || !ok {
		t.Fatalf("could not hold lock: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = l.Unlock() })

	result, err := cleanBookkeepingDetect(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(result.StaleProvisioning) != 0 {
		t.Fatalf("StaleProvisioning = %v, want empty (lock is held)", result.StaleProvisioning)
	}
}

// TestCleanBookkeepingDetectSweepsProvisioningWhenLockFree: a
// .wrk-provisioning whose flock nobody holds is stale scratch and must
// appear in the plan.
func TestCleanBookkeepingDetectSweepsProvisioningWhenLockFree(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prov := filepath.Join(resourceDir, "5fd1d0d6.wrk-provisioning")
	if err := os.MkdirAll(prov, 0o755); err != nil {
		t.Fatal(err)
	}
	// No lock file, no holder — stale.

	result, err := cleanBookkeepingDetect(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(result.StaleProvisioning) != 1 || result.StaleProvisioning[0] != prov {
		t.Fatalf("StaleProvisioning = %v, want [%q]", result.StaleProvisioning, prov)
	}
}

// TestCleanBookkeepingDetectFindsDeletingMarker: partial-delete markers
// from a crashed prior gc are always safe to sweep.
func TestCleanBookkeepingDetectFindsDeletingMarker(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	del := filepath.Join(resourceDir, "5fd1d0d6.wrk-deleting")
	if err := os.MkdirAll(del, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := cleanBookkeepingDetect(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(result.StaleDeleting) != 1 || result.StaleDeleting[0] != del {
		t.Fatalf("StaleDeleting = %v, want [%q]", result.StaleDeleting, del)
	}
}

// TestCleanBookkeepingDetectEmptyStorage: a repo whose storage tree
// doesn't exist yet must return an empty plan with no error.
func TestCleanBookkeepingDetectEmptyStorage(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	result, err := cleanBookkeepingDetect(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(result.OrphanedLocks)+len(result.StaleProvisioning)+len(result.StaleDeleting) != 0 {
		t.Fatalf("expected empty result, got %+v", result)
	}
}
