package engine

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"
)

// TestExecuteGCDeletesUnpinnedVariant: after Link mints a second
// fingerprint variant, ExecuteGC removes the first (now unpinned) one.
// A second BuildGCPlan on the already-executed state must HasNothing —
// this pins the idempotence + lock-file-cleanup contract.
func TestExecuteGCDeletesUnpinnedVariant(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\" && touch \"{shared}/.installed\"'\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link v1: %v", err)
	}
	writeFile(t, filepath.Join(repo.Root, "package.json"), `{"v":2}`)
	_ = os.Remove(filepath.Join(repo.Root, "node_modules"))
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link v2: %v", err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.DeleteVariants) != 1 {
		t.Fatalf("expected 1 variant to delete, got %d", len(plan.DeleteVariants))
	}
	toDelete := plan.DeleteVariants[0].StoragePath

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	if _, err := os.Stat(toDelete); !os.IsNotExist(err) {
		t.Errorf("variant survived deletion: %s", toDelete)
	}

	// Second run should be a no-op.
	plan2, _ := BuildGCPlan(repo, Options{StorageRoot: storage})
	if !plan2.HasNothing() {
		t.Errorf("second BuildGCPlan not empty: %+v", plan2)
	}
}

// TestExecuteGCSkipsHeldLock: when a concurrent process holds the
// variant lock, ExecuteGC must skip that variant with a warning on
// options.Stdout rather than tear data out from under the peer.
func TestExecuteGCSkipsHeldLock(t *testing.T) {
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
		t.Fatalf("Link v1: %v", err)
	}
	writeFile(t, filepath.Join(repo.Root, "package.json"), `{"v":2}`)
	_ = os.Remove(filepath.Join(repo.Root, "node_modules"))
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link v2: %v", err)
	}

	plan, _ := BuildGCPlan(repo, Options{StorageRoot: storage})
	if len(plan.DeleteVariants) != 1 {
		t.Fatalf("expected 1 delete variant, got %d", len(plan.DeleteVariants))
	}
	target := plan.DeleteVariants[0].StoragePath
	lockPath := target + ".wrk-lock"

	// Hold the lock during ExecuteGC.
	l := flock.New(lockPath)
	ok, err := l.TryLock()
	if err != nil || !ok {
		t.Fatalf("could not hold lock: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = l.Unlock() })

	var buf bytes.Buffer
	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &buf}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	if _, err := os.Stat(target); err != nil {
		t.Errorf("held-lock variant should have survived: %v", err)
	}
	if !strings.Contains(buf.String(), "lock held") {
		t.Errorf("expected warning about held lock, got %q", buf.String())
	}
}

// TestExecuteGCSweepsDeletingMarker: a .wrk-deleting dir seeded to
// mimic a crashed prior gc must be cleaned up by ExecuteGC. Covers the
// "walk finds cruft the executor sweeps" side of the bookkeeping list.
func TestExecuteGCSweepsDeletingMarker(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	// Seed a stale .wrk-deleting marker as if a prior GC crashed.
	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleMarker := filepath.Join(resourceDir, "5fd1d0d6.wrk-deleting")
	if err := os.MkdirAll(staleMarker, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.StaleDeleting) != 1 {
		t.Fatalf("expected 1 stale deleting marker, got %d", len(plan.StaleDeleting))
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}
	if _, err := os.Stat(staleMarker); !os.IsNotExist(err) {
		t.Errorf("stale marker survived: %v", err)
	}
}

// TestExecuteGCPrunesGhostAndRegistryEntry: PruneGhosts must run
// BEFORE pruneOrphanRegistryEntries so the registry sweep sees the
// post-prune live-workspace set. Covers step ordering 1 → 2.
func TestExecuteGCPrunesGhostAndRegistryEntry(t *testing.T) {
	skipIfNoGit(t)
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})

	// Create a real ghost worktree so DetectGhosts sees it.
	tempParent := filepath.Dir(repo.Root)
	feature := filepath.Join(tempParent, "feature")
	cmd := exec.Command("git", "-C", repo.Root, "worktree", "add", "-b", "feature", feature)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := os.RemoveAll(feature); err != nil {
		t.Fatal(err)
	}

	// Seed a stray registry entry.
	reg, _ := loadRegistry(repo)
	reg[feature] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storageIn(t, repo.Root)})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storageIn(t, repo.Root), Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	// git worktree list should not mention feature anymore.
	out, err := exec.Command("git", "-C", repo.Root, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "feature") {
		t.Errorf("ghost survived prune:\n%s", out)
	}

	// Registry entry should be gone.
	after, _ := loadRegistry(repo)
	if _, ok := after[feature]; ok {
		t.Errorf("orphan registry entry survived")
	}
}

// TestExecuteGCSweepsCrashedForgetMarker: `wrk gc` sweeps a
// <repo-id>.wrk-forgetting/ marker left by a crashed `wrk forget`.
// The marker sits at the storage root as a sibling of the repo-id
// subtree — verifies cleanBookkeepingDetect finds it and ExecuteGC
// removes it.
func TestExecuteGCSweepsCrashedForgetMarker(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	marker := filepath.Join(storage, repo.RepositoryID+".wrk-forgetting")
	if err := os.MkdirAll(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(marker, "leftover"), "stale")

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.StaleForgetting) != 1 {
		t.Fatalf("StaleForgetting = %v, want 1", plan.StaleForgetting)
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("forgetting marker survived gc: %v", err)
	}
}

// TestExecuteGCInvokesProgress pins the plumbing that fires
// options.Progress during a variant delete. After Link mints a
// second variant, the first (now-unpinned) variant contains at
// least the initialize hook's touched marker; RemoveAllProgress
// must fire the callback for each regular file it removes.
func TestExecuteGCInvokesProgress(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\" && dd if=/dev/zero of=\"{shared}/blob\" bs=1024 count=4 2>/dev/null'\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link v1: %v", err)
	}
	writeFile(t, filepath.Join(repo.Root, "package.json"), `{"v":2}`)
	_ = os.Remove(filepath.Join(repo.Root, "node_modules"))
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link v2: %v", err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.DeleteVariants) != 1 {
		t.Fatalf("expected 1 variant to delete, got %d", len(plan.DeleteVariants))
	}

	var total int64
	opts := Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
		Progress:    func(n int64) { total += n },
	}
	if err := ExecuteGC(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}
	// The dd blob is 4 KiB and the variant subtree is renamed then
	// removed via RemoveAllProgress. A non-zero total proves the
	// callback actually fired for at least the blob file.
	if total <= 0 {
		t.Errorf("progress total = %d, want >0 (blob bytes)", total)
	}
}

// TestExecuteGCCompletesMidSwapCrash pins the B1 end-to-end: given
// the mid-swap-crash fingerprint (`.wrk-provisioning/` +
// `.wrk-deleting/` + missing real), ExecuteGC must promote the
// provisioning payload to real BEFORE the standard sweep runs, then
// let the sweep clean up the deleting sibling. If the promotion ran
// AFTER the sweep, the sweep would have already deleted the
// provisioning as stale and the hook's output would be gone.
//
// Post-conditions:
//   - variant real path exists AND contains the provisioning payload
//   - .wrk-provisioning/ is gone (renamed)
//   - .wrk-deleting/ is gone (swept)
func TestExecuteGCCompletesMidSwapCrash(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	variantBase := filepath.Join(resourceDir, "abc123")
	deletingPath := variantBase + ".wrk-deleting"
	provisioningPath := variantBase + ".wrk-provisioning"
	if err := os.MkdirAll(deletingPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deletingPath, "old-content.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(provisioningPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(provisioningPath, "new-content.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.PendingSwaps) != 1 {
		t.Fatalf("PendingSwaps = %d, want 1", len(plan.PendingSwaps))
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	// Real variant now exists and holds the promoted payload.
	promoted := filepath.Join(variantBase, "new-content.txt")
	got, err := os.ReadFile(promoted)
	if err != nil {
		t.Fatalf("read promoted content at %s: %v", promoted, err)
	}
	if string(got) != "new" {
		t.Errorf("promoted content = %q, want %q", string(got), "new")
	}

	// Provisioning is gone (renamed).
	if _, err := os.Stat(provisioningPath); !os.IsNotExist(err) {
		t.Errorf("provisioning still present: %v", err)
	}
	// Deleting is gone (swept by the standard sweep phase).
	if _, err := os.Stat(deletingPath); !os.IsNotExist(err) {
		t.Errorf("deleting sibling still present: %v", err)
	}
}

// TestExecuteGCSweepsOrphanedIsolationEntries pins the B2 end-to-end:
// isolation-registry entries whose workspace root has vanished MUST
// be cleared from `isolated.json` after ExecuteGC. Live entries MUST
// survive; a ghost-only sweep is the whole point.
func TestExecuteGCSweepsOrphanedIsolationEntries(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	// One live entry (the repo itself is a live workspace) and two
	// ghost entries pointing at a workspace root that never existed
	// / has been rm -rf'd.
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/live-nm"); err != nil {
		t.Fatalf("recordIsolation live: %v", err)
	}
	ghostRoot := filepath.Join(filepath.Dir(repo.Root), "ghost-worktree")
	if err := recordIsolation(repo, ghostRoot, "node_modules", "/storage/ghost-nm"); err != nil {
		t.Fatalf("recordIsolation ghost nm: %v", err)
	}
	if err := recordIsolation(repo, ghostRoot, "vendor/bundle", "/storage/ghost-vb"); err != nil {
		t.Fatalf("recordIsolation ghost vb: %v", err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.OrphanedIsolationEntries) != 2 {
		t.Fatalf("OrphanedIsolationEntries = %d, want 2: %+v",
			len(plan.OrphanedIsolationEntries), plan.OrphanedIsolationEntries)
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	// Ghost entries gone; live entry survives.
	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := reg[ghostRoot]; ok {
		t.Errorf("ghost workspace still in registry after ExecuteGC: %+v", reg[ghostRoot])
	}
	if _, ok := isIsolated(reg, repo.Root, "node_modules"); !ok {
		t.Errorf("live isolation entry was swept — B2 must scope to orphans only")
	}

	// Idempotence: a second BuildGCPlan is empty on this axis.
	plan2, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan (post): %v", err)
	}
	if len(plan2.OrphanedIsolationEntries) != 0 {
		t.Errorf("post-execute OrphanedIsolationEntries = %+v, want empty",
			plan2.OrphanedIsolationEntries)
	}
}

// TestGCSweepsOrphanedIsolatedVariantSameRun pins single-run
// convergence: when an isolation entry's workspace root is gone, the
// SAME gc run that clears the registry entry must also delete the
// isolated variant it pointed at. Pinning orphaned entries would make
// the variant survive until a second run — "I gc'd, why is it still
// there?"
func TestGCSweepsOrphanedIsolatedVariantSameRun(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)

	// A real isolated variant dir in storage, claimed only by a
	// workspace root that does not exist on disk.
	isolated := filepath.Join(storage, repo.RepositoryID, "node_modules", "isolated-deadbeef1234")
	if err := os.MkdirAll(isolated, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(isolated, "marker"), "orphaned\n")
	ghostRoot := filepath.Join(filepath.Dir(repo.Root), "ghost-worktree")
	if err := recordIsolation(repo, ghostRoot, "node_modules", isolated); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.OrphanedIsolationEntries) != 1 {
		t.Fatalf("OrphanedIsolationEntries = %+v, want the ghost entry",
			plan.OrphanedIsolationEntries)
	}
	found := false
	for _, v := range plan.DeleteVariants {
		if v.StoragePath == isolated {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphaned isolated variant not slated for deletion; DeleteVariants = %+v",
			plan.DeleteVariants)
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	// Single-run convergence: variant gone from disk...
	if _, err := os.Stat(isolated); !os.IsNotExist(err) {
		t.Errorf("orphaned isolated variant survived the same gc run: %s", isolated)
	}
	// ...and the registry entry cleared.
	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := reg[ghostRoot]; ok {
		t.Errorf("ghost registry entry survived ExecuteGC: %+v", reg[ghostRoot])
	}
}

// TestGCKeepsIsolatedVariantForLiveWorkspace is the counter-case: an
// isolation entry whose workspace root EXISTS keeps its pin, even when
// no workspace symlink currently resolves into the variant (the
// registry is the authoritative claim). The variant must survive gc.
func TestGCKeepsIsolatedVariantForLiveWorkspace(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)

	isolated := filepath.Join(storage, repo.RepositoryID, "node_modules", "isolated-cafe0123abcd")
	if err := os.MkdirAll(isolated, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(isolated, "marker"), "claimed\n")
	// Live claim: repo.Root exists on disk.
	if err := recordIsolation(repo, repo.Root, "node_modules", isolated); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	for _, v := range plan.DeleteVariants {
		if v.StoragePath == isolated {
			t.Fatalf("live isolated variant slated for deletion: %+v", plan.DeleteVariants)
		}
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}
	if _, err := os.Stat(isolated); err != nil {
		t.Errorf("live isolated variant did not survive gc: %v", err)
	}
}
