package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

func TestIsolationRegistryRoundTrip(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/foo/isolated-abc"); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	entry, ok := isIsolated(reg, repo.Root, "node_modules")
	if !ok {
		t.Fatal("entry missing after record")
	}
	if entry.StoragePath != "/storage/foo/isolated-abc" {
		t.Errorf("StoragePath = %q, want /storage/foo/isolated-abc", entry.StoragePath)
	}
	if entry.CreatedAt == "" {
		t.Errorf("CreatedAt empty; want RFC3339 timestamp")
	}
}

// TestIsolationRegistryOverwritesExistingEntry pins the contract that
// re-recording the same (workspace, resource) pair replaces the prior
// storage path — the caller (RelinkIsolate on the same resource twice)
// is authoritative about which variant is pinned right now.
func TestIsolationRegistryOverwritesExistingEntry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/foo/iso-1"); err != nil {
		t.Fatalf("first recordIsolation: %v", err)
	}
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/foo/iso-2"); err != nil {
		t.Fatalf("second recordIsolation: %v", err)
	}
	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	entry, ok := isIsolated(reg, repo.Root, "node_modules")
	if !ok {
		t.Fatal("entry missing after overwrite")
	}
	if entry.StoragePath != "/storage/foo/iso-2" {
		t.Errorf("StoragePath = %q, want /storage/foo/iso-2", entry.StoragePath)
	}
}

func TestIsolationClearRemovesEntry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/x"); err != nil {
		t.Fatalf("record node_modules: %v", err)
	}
	if err := recordIsolation(repo, repo.Root, "vendor/bundle", "/storage/y"); err != nil {
		t.Fatalf("record vendor/bundle: %v", err)
	}

	if err := clearIsolation(repo, repo.Root, "node_modules"); err != nil {
		t.Fatalf("clearIsolation: %v", err)
	}
	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(reg, repo.Root, "node_modules"); ok {
		t.Errorf("cleared entry still present")
	}
	if _, ok := isIsolated(reg, repo.Root, "vendor/bundle"); !ok {
		t.Errorf("unrelated entry was dropped")
	}

	// Clearing the last entry removes the workspace key entirely, so
	// the registry does not accumulate empty stubs across churn.
	if err := clearIsolation(repo, repo.Root, "vendor/bundle"); err != nil {
		t.Fatalf("clearIsolation (2nd): %v", err)
	}
	reg, err = loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation (post-empty): %v", err)
	}
	if _, ok := reg[repo.Root]; ok {
		t.Errorf("empty workspace key survived")
	}
}

func TestIsolationClearMissingIsNoop(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	// Clearing from an empty registry — the file does not exist yet.
	if err := clearIsolation(repo, repo.Root, "never-set"); err != nil {
		t.Fatalf("clearIsolation on empty registry: %v", err)
	}

	// Clearing an unknown resource from a workspace that has other
	// entries: the workspace key MUST survive, and the on-disk file
	// MUST NOT be rewritten with an empty map.
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/x"); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}
	if err := clearIsolation(repo, repo.Root, "never-set"); err != nil {
		t.Fatalf("clearIsolation of missing resource: %v", err)
	}
	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(reg, repo.Root, "node_modules"); !ok {
		t.Errorf("unrelated resource was dropped by no-op clear")
	}

	// Clearing from a workspace with no entries at all — but the
	// registry file exists (populated by another workspace).
	other := repo.Root + "-feature"
	if err := clearIsolation(repo, other, "anything"); err != nil {
		t.Fatalf("clearIsolation on unknown workspace: %v", err)
	}
}

func TestIsolationTargetsAcrossWorkspaces(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/A/iso-1"); err != nil {
		t.Fatal(err)
	}
	if err := recordIsolation(repo, repo.Root+"-feature", "vendor/bundle", "/storage/B/iso-2"); err != nil {
		t.Fatal(err)
	}

	targets, err := isolationTargets(repo)
	if err != nil {
		t.Fatalf("isolationTargets: %v", err)
	}
	sort.Strings(targets)
	want := []string{"/storage/A/iso-1", "/storage/B/iso-2"}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("targets[%d] = %q, want %q", i, targets[i], want[i])
		}
	}
}

func TestIsolationTargetsEmptyOnClean(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	targets, err := isolationTargets(repo)
	if err != nil {
		t.Fatalf("isolationTargets: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("targets = %v, want empty", targets)
	}
}

func TestIsolationCorruptFileTreatedAsEmpty(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	path := isolationPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json {"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if len(reg) != 0 {
		t.Errorf("expected empty registry, got %+v", reg)
	}

	// After the corrupt-tolerant load, a subsequent record MUST succeed
	// and replace the garbage file with a valid one — the load path is
	// forgiving, but the save path still writes clean JSON.
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/x"); err != nil {
		t.Fatalf("recordIsolation after corrupt load: %v", err)
	}
	reg, err = loadIsolation(repo)
	if err != nil {
		t.Fatalf("post-record loadIsolation: %v", err)
	}
	if _, ok := isIsolated(reg, repo.Root, "node_modules"); !ok {
		t.Errorf("record after corrupt load did not persist")
	}
}

// TestIsolationConcurrentWithDetach pins the cross-registry serialization
// contract: isolate and detach share `withRegistryLock`, so a concurrent
// pair against the same repo must both survive. Neither's file may be
// clobbered by the other's atomic rename, and neither may observe a
// half-committed state through the shared flock.
func TestIsolationConcurrentWithDetach(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

	const iters = 30
	for i := range iters {
		var wg sync.WaitGroup
		var isoErr, detErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			isoErr = recordIsolation(repo, repo.Root, "node_modules", "/storage/A/iso")
		}()
		go func() {
			defer wg.Done()
			detErr = recordDetached(repo, []string{".env"})
		}()
		wg.Wait()

		if isoErr != nil {
			t.Fatalf("iteration %d: recordIsolation: %v", i, isoErr)
		}
		if detErr != nil {
			t.Fatalf("iteration %d: recordDetached: %v", i, detErr)
		}

		iso, err := loadIsolation(repo)
		if err != nil {
			t.Fatalf("iteration %d: loadIsolation: %v", i, err)
		}
		if _, ok := isIsolated(iso, repo.Root, "node_modules"); !ok {
			t.Fatalf("iteration %d: isolation was lost", i)
		}
		detach, err := loadRegistry(repo)
		if err != nil {
			t.Fatalf("iteration %d: loadRegistry: %v", i, err)
		}
		found := false
		for _, p := range detach[repo.Root] {
			if p == ".env" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("iteration %d: detach was lost", i)
		}

		// Reset for the next iteration — clearIsolation drops the
		// isolation entry, clearDetached drops the whole workspace's
		// detach entry.
		if err := clearIsolation(repo, repo.Root, "node_modules"); err != nil {
			t.Fatalf("iteration %d: clearIsolation: %v", i, err)
		}
		if err := clearDetached(repo); err != nil {
			t.Fatalf("iteration %d: clearDetached: %v", i, err)
		}
	}
}

func TestIsolationJSONFormatIsHumanReadable(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	if err := recordIsolation(repo, repo.Root, "node_modules", "/storage/foo"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(isolationPath(repo))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON:\n%s\n%v", data, err)
	}
	// Indented output makes on-disk debugging tractable — the top-level
	// map opens with a newline followed by two-space indent.
	if !contains(string(data), "\n  ") {
		t.Errorf("expected indented JSON, got:\n%s", data)
	}
	// The atomic-write path uses a sibling tmp file; a successful record
	// must leave nothing behind but the real registry file.
	tmp := isolationPath(repo) + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp file %s survived rename (err=%v)", tmp, err)
	}
}

// TestIsolationConcurrentWithDetachCrossWorkspaces defends the
// cross-workspace flock contract for the shared registry file. Two
// git worktrees of the same repo point at the SAME
// `<metadata>/wrk/detached.json` and `<metadata>/wrk/isolated.json`
// (git's --git-common-dir shares them). Isolate on the primary and
// detach on the secondary go through withRegistryLock against the
// same on-disk lock file, so their load-modify-atomic-rename cycles
// MUST serialize even when the OS process, workspace root, and the
// operation type differ.
//
// Without the flock, either operation's rename can clobber the
// other's file — the isolation entry silently vanishes, the detach
// entry silently vanishes, or on macOS's rename-is-swap semantics
// both survive by accident and hide the race. Under the flock, both
// entries survive every iteration, keyed against their respective
// workspace roots. Mirrors TestClearDetachedConcurrentWithRecord's
// iteration count so a broken lock reliably fails within seconds.
//
// Cross-workspace over cross-registry: TestIsolationConcurrentWithDetach
// covers same-repo, same-workspace concurrent isolate+detach; this
// test covers the sibling-worktree case where the workspace keys
// differ so a broken lock silently drops one workspace's entry.
func TestIsolationConcurrentWithDetachCrossWorkspaces(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})
	_, secondary := addGitWorktree(t, primary, "wt-b")

	if primary.MetadataDir() != secondary.MetadataDir() {
		t.Fatalf(
			"test precondition broken: worktrees have separate metadata dirs (%q vs %q)",
			primary.MetadataDir(), secondary.MetadataDir(),
		)
	}

	const iters = 30
	for i := range iters {
		pathA := "node_modules"
		pathB := ".env"
		storagePathA := fmt.Sprintf("/storage/iso-A-%d", i)

		// Reset both registries so each iteration starts from a
		// clean slate — otherwise a stale entry from iteration N-1
		// would satisfy the "did the entry survive?" assertion
		// vacuously.
		if err := os.RemoveAll(isolationPath(primary)); err != nil {
			t.Fatalf("iteration %d: reset isolation: %v", i, err)
		}
		if err := os.RemoveAll(registryPath(primary)); err != nil {
			t.Fatalf("iteration %d: reset detach: %v", i, err)
		}

		var wg sync.WaitGroup
		var isoErr, detErr error
		wg.Add(2)

		go func() {
			defer wg.Done()
			isoErr = recordIsolation(primary, primary.Root, pathA, storagePathA)
		}()
		go func() {
			defer wg.Done()
			// recordDetached keys against repo.Root, so writing via
			// `secondary` records under the secondary worktree's
			// root — a distinct key from primary.Root, which is
			// what makes this the cross-workspace race.
			detErr = recordDetached(secondary, []string{pathB})
		}()

		wg.Wait()

		if isoErr != nil {
			t.Fatalf("iteration %d: recordIsolation: %v", i, isoErr)
		}
		if detErr != nil {
			t.Fatalf("iteration %d: recordDetached: %v", i, detErr)
		}

		iso, err := loadIsolation(primary)
		if err != nil {
			t.Fatalf("iteration %d: loadIsolation: %v", i, err)
		}
		entry, ok := isIsolated(iso, primary.Root, pathA)
		if !ok {
			t.Fatalf("iteration %d: isolation entry for %s missing — clobbered by concurrent detach",
				i, primary.Root)
		}
		if entry.StoragePath != storagePathA {
			t.Errorf("iteration %d: StoragePath = %q, want %q",
				i, entry.StoragePath, storagePathA)
		}

		reg, err := loadRegistry(primary)
		if err != nil {
			t.Fatalf("iteration %d: loadRegistry: %v", i, err)
		}
		found := false
		for _, p := range reg[secondary.Root] {
			if p == pathB {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("iteration %d: detach entry for %s (path %q) missing — clobbered by concurrent isolate",
				i, secondary.Root, pathB)
		}
	}
}
