package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestBuildForgetPlanEmptyRepo(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storageIn(t, repo.Root)})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if plan.RepositoryID != repo.RepositoryID {
		t.Errorf("RepositoryID = %q, want %q", plan.RepositoryID, repo.RepositoryID)
	}
	if plan.TotalSize != 0 || plan.VariantCount != 0 {
		t.Errorf("expected empty storage; got TotalSize=%d VariantCount=%d",
			plan.TotalSize, plan.VariantCount)
	}
	if plan.Refusal != "" {
		t.Errorf("Refusal = %q, want empty", plan.Refusal)
	}
	// StoragePath is populated even for a missing subtree so the
	// executor has a target to noop against.
	wantStorage := filepath.Join(storageIn(t, repo.Root), repo.RepositoryID)
	if plan.StoragePath != wantStorage {
		t.Errorf("StoragePath = %q, want %q", plan.StoragePath, wantStorage)
	}
}

func TestBuildForgetPlanCountsAfterLink(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: node\n    path: node_modules\n    fingerprint:\n      - \"{root}/package.json\"\n    hooks:\n      initialize:\n        - run: sh -c 'mkdir -p \"{shared}\" && dd if=/dev/zero of=\"{shared}/blob\" bs=1024 count=1'\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if plan.VariantCount != 1 {
		t.Errorf("VariantCount = %d, want 1", plan.VariantCount)
	}
	if plan.ResourceCount != 1 {
		t.Errorf("ResourceCount = %d, want 1", plan.ResourceCount)
	}
	if plan.TotalSize <= 0 {
		t.Errorf("TotalSize = %d, want >0", plan.TotalSize)
	}
	if plan.Refusal != "" {
		t.Errorf("Refusal = %q, want empty", plan.Refusal)
	}
}

func TestBuildForgetPlanRegistryEntriesTriggerRefusal(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	// Seed a detach entry.
	reg, _ := loadRegistry(repo)
	reg[repo.Root] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storageIn(t, repo.Root)})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if plan.Refusal == "" {
		t.Fatal("expected refusal for populated registry")
	}
	if !strings.Contains(plan.Refusal, "detached") {
		t.Errorf("Refusal = %q, want mention of 'detached'", plan.Refusal)
	}
	if len(plan.RegistryEntries) != 1 {
		t.Errorf("RegistryEntries = %v, want 1 entry", plan.RegistryEntries)
	}
	if got := plan.RegistryEntries[repo.Root]; len(got) != 1 || got[0] != "node_modules" {
		t.Errorf("RegistryEntries[%q] = %v, want [node_modules]", repo.Root, got)
	}
}

func TestBuildForgetPlanIsReadOnly(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	before := readTreeSnapshot(t, filepath.Join(storage, repo.RepositoryID))

	if _, err := BuildForgetPlan(repo, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}

	after := readTreeSnapshot(t, filepath.Join(storage, repo.RepositoryID))
	if before != after {
		t.Fatalf("BuildForgetPlan mutated the tree\nbefore=%s\nafter=%s", before, after)
	}
}

// readTreeSnapshot returns a sorted list of every path under root.
// Use as a simple invariant check; tolerate missing trees (returns "").
func readTreeSnapshot(t *testing.T, root string) string {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return ""
	}
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}

func TestExecuteForgetRemovesStorageAndClearsRegistry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Seed a detach entry so the executor also clears it.
	reg, _ := loadRegistry(repo)
	reg[repo.Root] = []string{".env"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}

	if _, err := os.Stat(plan.StoragePath); !os.IsNotExist(err) {
		t.Errorf("storage tree survived: %v", err)
	}
	// The .wrk-forgetting marker MUST also be gone — a leftover would
	// mean step 2 (RemoveAll) silently failed.
	if _, err := os.Stat(plan.StoragePath + ".wrk-forgetting"); !os.IsNotExist(err) {
		t.Errorf(".wrk-forgetting marker survived: %v", err)
	}
	after, _ := loadRegistry(repo)
	if len(after) != 0 {
		t.Errorf("registry not cleared: %v", after)
	}
	// .wrk.yml stays — forget removes shared storage, not the config.
	if _, err := os.Stat(filepath.Join(repo.Root, ".wrk.yml")); err != nil {
		t.Errorf(".wrk.yml missing after forget: %v", err)
	}
}

func TestExecuteForgetIdempotentAfterCrash(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	// Seed a crashed .wrk-forgetting marker directly — simulate a
	// prior forget that got SIGKILL'd between rename and RemoveAll.
	storagePath := filepath.Join(storage, repo.RepositoryID)
	marker := storagePath + ".wrk-forgetting"
	if err := os.MkdirAll(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(marker, "leftover"), "stale")

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("forgetting marker survived: %v", err)
	}
}

func TestExecuteForgetIdempotentRerun(t *testing.T) {
	// A second ExecuteForget on the same plan MUST be a no-op —
	// storage already gone, registry already empty.
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget first: %v", err)
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget second: %v", err)
	}
}

func TestExecuteForgetEmptyStorageIsNoop(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	storage := storageIn(t, repo.Root)
	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget on empty repo: %v", err)
	}
}

func TestExecuteForgetSubsequentLinkReprovisions(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link initial: %v", err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}

	// The Link-installed workspace symlink is now dangling — Link
	// itself does not un-detach, so we clean up the way a real user
	// would before re-linking.
	_ = os.Remove(filepath.Join(repo.Root, ".env"))
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed-again\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link post-forget: %v", err)
	}
	sharedEnv := filepath.Join(storage, repo.RepositoryID, ".env")
	if _, err := os.Stat(sharedEnv); err != nil {
		t.Errorf("shared file missing post-forget re-link: %v", err)
	}
}

func TestExecuteForgetClearsMultiWorkspaceRegistry(t *testing.T) {
	// A registry with entries from multiple workspaces must be
	// entirely cleared — forget is a wholesale nuke.
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	reg, _ := loadRegistry(repo)
	reg["/tmp/ws-a"] = []string{".env"}
	reg["/tmp/ws-b"] = []string{"cfg.toml"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatal(err)
	}

	// BuildForgetPlan refuses when the registry is non-empty; the
	// executor is called only past a --force gate, so we skip the
	// plan builder and construct the plan directly.
	plan := ForgetPlan{
		RepositoryID: repo.RepositoryID,
		StoragePath:  filepath.Join(storage, repo.RepositoryID),
	}
	if err := ExecuteForget(repo, plan, Options{StorageRoot: storage}); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}
	after, _ := loadRegistry(repo)
	if len(after) != 0 {
		t.Errorf("registry not fully cleared: %v", after)
	}
}

// TestExecuteForgetInvokesProgress pins the plumbing that fires
// options.Progress during the trailing storage sweep. Link mints a
// non-empty storage tree containing an initialize-hook blob;
// ExecuteForget MUST call the callback with a positive byte total
// as it removes every regular file.
func TestExecuteForgetInvokesProgress(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: node\n    path: node_modules\n" +
			"    fingerprint:\n      - \"{root}/package.json\"\n" +
			"    hooks:\n      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\" && dd if=/dev/zero of=\"{shared}/blob\" bs=1024 count=8 2>/dev/null'\n",
		"package.json": `{"v":1}`,
	})
	storage := storageIn(t, repo.Root)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if plan.TotalSize <= 0 {
		t.Fatalf("prerequisite: plan.TotalSize=%d, want >0", plan.TotalSize)
	}

	var total int64
	opts := Options{StorageRoot: storage, Progress: func(n int64) { total += n }}
	if err := ExecuteForget(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}
	if total <= 0 {
		t.Errorf("progress total = %d, want >0 (blob bytes)", total)
	}
}

// TestExecuteForgetRecoveryPathInvokesProgress pins the crashed-
// marker recovery branch also routes through RemoveAllProgress.
// A seeded .wrk-forgetting marker with content MUST fire the
// callback, otherwise the recovery path silently regresses to
// os.RemoveAll.
func TestExecuteForgetRecoveryPathInvokesProgress(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	storage := storageIn(t, repo.Root)

	storagePath := filepath.Join(storage, repo.RepositoryID)
	marker := storagePath + ".wrk-forgetting"
	if err := os.MkdirAll(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fixed size for a stable lower-bound assertion.
	if err := os.WriteFile(filepath.Join(marker, "leftover"), make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildForgetPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}

	var total int64
	opts := Options{StorageRoot: storage, Progress: func(n int64) { total += n }}
	if err := ExecuteForget(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}
	if total < 512 {
		t.Errorf("progress total = %d, want >= 512 (leftover blob)", total)
	}
}
