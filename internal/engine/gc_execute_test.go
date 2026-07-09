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
