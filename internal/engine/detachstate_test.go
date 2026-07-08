package engine

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blaineventurine/wrk/internal/repository"
)

// TestWithRegistryLockCreatesParentDir pins the flock plumbing: the
// lock helper creates the metadata dir on demand so a fresh repo
// (whose `.git/wrk/` has never held a registry) can still acquire the
// lock and record intent. Regression against a naive implementation
// that would ENOENT out on the first-ever detach.
func TestWithRegistryLockCreatesParentDir(t *testing.T) {
	repo := newTestRepo(t)

	// Sanity: the metadata subdir should not exist yet.
	dir := filepath.Dir(registryPath(repo))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("baseline: %s already exists (err=%v)", dir, err)
	}

	called := false
	if err := withRegistryLock(repo, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("withRegistryLock: %v", err)
	}
	if !called {
		t.Fatal("withRegistryLock did not invoke fn")
	}

	// Lock file was created (and released) under the metadata dir.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected metadata dir created: %v", err)
	}
	lockPath := registryPath(repo) + ".wrk-lock"
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file created: %v", err)
	}
}

// TestRecordDetachedConcurrentAcrossWorkspaces pins C1: two workspaces
// of the same repo share `.git/wrk/detached.json` via
// `git --git-common-dir`. Without the flock, concurrent load-modify-
// atomic-rename cycles can lose an entry — the second writer's rename
// replaces the first writer's file, silently dropping the first
// workspace's key. Under the flock, both entries always survive.
//
// N goroutines with N-different Root keys hammering the same registry
// file across parallel iterations gives the race a large enough window
// that a broken implementation reliably drops entries within a few
// hundred rounds; a correct implementation never does.
func TestRecordDetachedConcurrentAcrossWorkspaces(t *testing.T) {
	primary := newTestRepoWithHead(t, nil)
	_, secondary := addGitWorktree(t, primary, "wt-b")

	// Both handles MUST resolve to the same shared metadata dir —
	// that's what makes this a race in the first place. If a future
	// refactor separates worktree state we would silently lose the
	// coverage this test provides, so guard the assumption.
	if primary.MetadataDir() != secondary.MetadataDir() {
		t.Fatalf(
			"test precondition broken: worktrees have separate metadata dirs (%q vs %q); "+
				"the C1 race only exists when they share the registry file",
			primary.MetadataDir(), secondary.MetadataDir(),
		)
	}
	if primary.Root == secondary.Root {
		t.Fatalf("test precondition broken: worktrees share Root %q", primary.Root)
	}

	const iterations = 50
	for i := range iterations {
		// Clean slate each iteration — otherwise the union semantics
		// would mask a lost write from the round before.
		if err := os.RemoveAll(registryPath(primary)); err != nil {
			t.Fatalf("reset registry: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		errs := make(chan error, 2)

		go func() {
			defer wg.Done()
			if err := recordDetached(primary, []string{"a.env"}); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := recordDetached(secondary, []string{"b.env"}); err != nil {
				errs <- err
			}
		}()

		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("iteration %d: recordDetached: %v", i, err)
		}

		reg := readRegistry(t, primary)
		gotA := sortedRegistryEntry(reg, primary.Root)
		gotB := sortedRegistryEntry(reg, secondary.Root)
		if wantA := []string{"a.env"}; !equalSlice(gotA, wantA) {
			t.Fatalf(
				"iteration %d: primary entry = %v, want %v — flock is missing, secondary rename clobbered primary write",
				i, gotA, wantA,
			)
		}
		if wantB := []string{"b.env"}; !equalSlice(gotB, wantB) {
			t.Fatalf(
				"iteration %d: secondary entry = %v, want %v — flock is missing, primary rename clobbered secondary write",
				i, gotB, wantB,
			)
		}
	}
}

// TestClearDetachedConcurrentWithRecord defends the mirror invariant:
// while one workspace clears its own entry, a sibling workspace
// concurrently recording a new entry must not be lost. Both operations
// mutate the same file; only the flock forces them to serialize.
func TestClearDetachedConcurrentWithRecord(t *testing.T) {
	primary := newTestRepoWithHead(t, nil)
	_, secondary := addGitWorktree(t, primary, "wt-b")

	if primary.MetadataDir() != secondary.MetadataDir() {
		t.Fatalf(
			"test precondition broken: worktrees have separate metadata dirs (%q vs %q)",
			primary.MetadataDir(), secondary.MetadataDir(),
		)
	}

	const iterations = 50
	for i := range iterations {
		// Seed: primary has an entry; secondary starts empty.
		if err := os.RemoveAll(registryPath(primary)); err != nil {
			t.Fatalf("reset registry: %v", err)
		}
		if err := recordDetached(primary, []string{"seed"}); err != nil {
			t.Fatalf("seed recordDetached: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		errs := make(chan error, 2)

		go func() {
			defer wg.Done()
			if err := clearDetached(primary); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := recordDetached(secondary, []string{"b.env"}); err != nil {
				errs <- err
			}
		}()

		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("iteration %d: %v", i, err)
		}

		reg := readRegistry(t, primary)

		// Primary key MUST be gone (clear won).
		if entry, ok := reg[primary.Root]; ok {
			t.Fatalf(
				"iteration %d: clearDetached did not remove primary entry: %v",
				i, entry,
			)
		}

		// Secondary key MUST be present with the recorded path
		// (record wasn't clobbered by clear's atomic rename).
		gotB := sortedRegistryEntry(reg, secondary.Root)
		if wantB := []string{"b.env"}; !equalSlice(gotB, wantB) {
			t.Fatalf(
				"iteration %d: secondary entry = %v, want %v — the record was silently dropped by concurrent clear",
				i, gotB, wantB,
			)
		}
	}
}

// TestWithRegistryLockSerializesSameTarget pins the raw serialization
// contract: while one caller is inside the lock, no other caller can
// enter. Peak concurrency observed inside the lock must never exceed
// one, no matter how many goroutines pile on.
//
// Matches the pattern used in internal/executor/lock_test.go so a
// future audit of flock semantics only needs to touch one shape.
func TestWithRegistryLockSerializesSameTarget(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	_, secondary := addGitWorktree(t, repo, "wt-b")

	// Both handles MUST point at the same lock file — otherwise the
	// test degrades into two independent locks and can't distinguish
	// serialized from parallel.
	if registryPath(repo) != registryPath(secondary) {
		t.Fatalf(
			"registry path differs between worktrees: %q vs %q",
			registryPath(repo), registryPath(secondary),
		)
	}

	repos := []*repository.Repository{repo, secondary}

	var (
		inside  atomic.Int32
		maxSeen atomic.Int32
		wg      sync.WaitGroup
	)

	for i := range 20 {
		wg.Add(1)
		r := repos[i%len(repos)]
		go func() {
			defer wg.Done()
			_ = withRegistryLock(r, func() error {
				n := inside.Add(1)
				for {
					old := maxSeen.Load()
					if n <= old || maxSeen.CompareAndSwap(old, n) {
						break
					}
				}
				inside.Add(-1)
				return nil
			})
		}()
	}

	wg.Wait()

	if got := maxSeen.Load(); got != 1 {
		t.Fatalf(
			"expected at most 1 goroutine inside withRegistryLock, saw %d — the flock is not serializing",
			got,
		)
	}
}
