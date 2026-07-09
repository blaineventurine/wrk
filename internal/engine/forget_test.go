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
