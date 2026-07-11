package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCleanupLifecycle exercises the whole cleanup surface end-to-end
// against a single fixture: provision two variants, detach one workspace,
// externally delete another, then wrk gc → wrk remove → wrk forget.
// A subsequent wrk link must recover cleanly.
func TestCleanupLifecycle(t *testing.T) {
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
	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	// 1) Link primary at v1.
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link v1: %v", err)
	}

	// 2) Change fingerprint input and re-link → v2 variant appears.
	writeFile(t, filepath.Join(repo.Root, "package.json"), `{"v":2}`)
	_ = os.Remove(filepath.Join(repo.Root, "node_modules"))
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link v2: %v", err)
	}

	// 3) Confirm two variants sit on disk.
	variants, err := scanVariants(repo, opts)
	if err != nil {
		t.Fatalf("scanVariants: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(variants))
	}

	// 4) wrk gc: prune the stale v1 variant, keep the pinned v2.
	plan, err := BuildGCPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.DeleteVariants) != 1 || len(plan.KeepVariants) != 1 {
		t.Fatalf("GC plan wrong: delete=%d keep=%d", len(plan.DeleteVariants), len(plan.KeepVariants))
	}
	if err := ExecuteGC(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}
	after, _ := scanVariants(repo, opts)
	if len(after) != 1 {
		t.Fatalf("after gc: expected 1 variant, got %d", len(after))
	}

	// 5) Create a feature workspace, then remove it via wrk remove.
	if err := NewWorkspace(repo, "feature", "", opts); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "feature")
	if _, err := os.Stat(feature); err != nil {
		t.Fatalf("feature dir missing after NewWorkspace: %v", err)
	}
	removePlan, err := BuildRemovePlan(repo, feature, opts)
	if err != nil {
		t.Fatalf("BuildRemovePlan(feature): %v", err)
	}
	if removePlan.Refusal != "" {
		t.Fatalf("clean feature should have no refusal, got %q", removePlan.Refusal)
	}
	if err := ExecuteRemove(repo, removePlan, false, opts); err != nil {
		t.Fatalf("ExecuteRemove: %v", err)
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Fatalf("feature dir survives ExecuteRemove: %v", err)
	}

	// 6) wrk forget: nuke the whole repo's storage.
	forgetPlan, err := BuildForgetPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if err := ExecuteForget(repo, forgetPlan, opts); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage, repo.RepositoryID)); !os.IsNotExist(err) {
		t.Errorf("storage subtree survives ExecuteForget")
	}

	// 7) After forget, re-link must succeed and re-create the variant.
	_ = os.Remove(filepath.Join(repo.Root, "node_modules"))
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link post-forget: %v", err)
	}
	postForget, _ := scanVariants(repo, opts)
	if len(postForget) != 1 {
		t.Errorf("post-forget Link should re-create exactly 1 variant, got %d", len(postForget))
	}

	// 8) Config file must still be present.
	if _, err := os.Stat(filepath.Join(repo.Root, ".wrk.yml")); err != nil {
		t.Errorf(".wrk.yml missing after full cleanup lifecycle: %v", err)
	}
}

// TestCleanupGhostSweepClearsRegistry pins the multi-step scenario where
// a user removes a worktree externally, leaving a stranded detach entry.
// wrk gc's ghost sweep must clear both the VCS metadata AND the registry
// entry.
func TestCleanupGhostSweepClearsRegistry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources: []\n",
	})
	storage := storageIn(t, repo.Root)
	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	// Create a feature workspace via wrk new.
	if err := NewWorkspace(repo, "ghost", "", opts); err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	feature := filepath.Join(filepath.Dir(repo.Root), "ghost")

	// Seed a registry entry for the ghost workspace.
	reg, _ := loadRegistry(repo)
	reg[feature] = []string{"node_modules"}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	// External rm -rf.
	if err := os.RemoveAll(feature); err != nil {
		t.Fatal(err)
	}

	// Now wrk gc: ghost should be pruned, registry entry cleared.
	plan, err := BuildGCPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.OrphanRegistry) == 0 {
		t.Errorf("gc plan missed orphan registry entry; plan=%+v", plan)
	}
	if err := ExecuteGC(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	after, _ := loadRegistry(repo)
	if _, ok := after[feature]; ok {
		t.Errorf("registry entry for ghost survived gc: %v", after)
	}

	// Verify the git worktree metadata is gone.
	live, err := repo.Workspaces()
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}
	for _, w := range live {
		if strings.HasSuffix(w, "/ghost") {
			t.Errorf("ghost worktree still listed as live: %s", w)
		}
	}
}
