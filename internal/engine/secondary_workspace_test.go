package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestLinkOnSecondaryWorkspaceSucceeds pins that Link works from a
// git-worktree-added SECONDARY workspace, not just the primary one.
// `wrk link` on a worktree drives the workspace-side actions from
// the worktree's Root and uses the SAME shared storage path derived
// from RepositoryID; the containment check follows ancestor
// symlinks and could false-positive if worktree-root plumbing
// regressed. A silent break here would take `wrk link` out of
// service for every user with more than one worktree — which is
// approximately every user.
//
// The task calls for in-workspace-storage-inside-feature; that
// exercises the case where the worktree carries its own storage
// tree (a realistic layout when users co-locate storage under the
// worktree via the CLI flag).
func TestLinkOnSecondaryWorkspaceSucceeds(t *testing.T) {
	// Track only the config; deliberately do NOT track .env so the
	// primary worktree has no .env and cannot conflict with the
	// secondary's shared storage from the primary side.
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	_, feature := addGitWorktree(t, primary, "feature")

	// The task specifies feature-side storage.
	storage := storageIn(t, feature.Root)

	// The external tool / developer creates the workspace file inside
	// the feature worktree only.
	writeFile(t, filepath.Join(feature.Root, ".env"), "feature=1\n")

	if err := Link(feature, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Link on secondary worktree: %v", err)
	}

	// The feature-side path is a symlink into shared storage — proof
	// Link's Symlink action ran against the worktree's Root.
	wsEnv := filepath.Join(feature.Root, ".env")
	info, err := os.Lstat(wsEnv)
	if err != nil {
		t.Fatalf("lstat feature/.env: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("feature/.env not a symlink after Link; mode=%v", info.Mode())
	}

	// Shared storage under the feature-side tree holds the exact bytes
	// we wrote to feature/.env — proof the Move action wrote to the
	// derived shared path and not to some primary-rooted default.
	sharedEnv := filepath.Join(storage, feature.RepositoryID, ".env")
	got, err := os.ReadFile(sharedEnv)
	if err != nil {
		t.Fatalf("read shared %s: %v", sharedEnv, err)
	}
	if string(got) != "feature=1\n" {
		t.Errorf("shared content = %q, want %q", got, "feature=1\n")
	}

	// And the symlink resolves to that shared path (verify link
	// target matches the absolute shared path so a `../` or otherwise
	// broken symlink target regression is caught).
	link, err := os.Readlink(wsEnv)
	if err != nil {
		t.Fatalf("readlink feature/.env: %v", err)
	}
	sharedAbs, err := filepath.Abs(sharedEnv)
	if err != nil {
		t.Fatal(err)
	}
	if link != sharedAbs {
		t.Errorf("symlink target = %q, want %q", link, sharedAbs)
	}
}

// TestDetachOnSecondaryWorkspaceRecordsInSecondaryRegistry pins the
// per-workspace isolation invariant of the detach registry. The
// registry is a map keyed by workspace Root and lives under
// MetadataDir/wrk/detached.json; git's --git-common-dir gives every
// worktree the SAME MetadataDir, so the isolation lives in the
// KEYS, not in separate files. Regardless of layout, after Detach
// on the secondary:
//
//   - the on-disk registry file MUST exist under the secondary's
//     MetadataDir (a directory-walk finds it — this passes with
//     git's shared metadata layout AND with a hypothetical future
//     per-worktree layout);
//   - the entry keyed by the SECONDARY's Root contains the detached
//     path;
//   - no entry is keyed by the primary's Root — the primary was not
//     touched by this Detach.
func TestDetachOnSecondaryWorkspaceRecordsInSecondaryRegistry(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	_, feature := addGitWorktree(t, primary, "feature")

	// Storage lives under the primary root — realistic layout where
	// all worktrees share one wrk storage tree.
	storage := storageIn(t, primary.Root)

	// Only the secondary has the workspace-side .env; that keeps
	// primary/.env absent and out of the picture for this test.
	writeFile(t, filepath.Join(feature.Root, ".env"), "secondary=1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(feature, opts); err != nil {
		t.Fatalf("Link on secondary: %v", err)
	}
	if err := Detach(feature, opts); err != nil {
		t.Fatalf("Detach on secondary: %v", err)
	}

	// (1) The registry file exists somewhere under feature's
	// MetadataDir. Walk the tree looking for wrk/detached.json.
	if !hasRegistryFile(t, feature.MetadataDir()) {
		t.Errorf("no wrk/detached.json under secondary MetadataDir %q",
			feature.MetadataDir())
	}

	// (2) The registry entry keyed by the SECONDARY's Root lists the
	// detached path. Load via the engine's own helper so the assertion
	// tracks the engine's on-disk contract, not a re-implementation.
	reg, err := loadRegistry(feature)
	if err != nil {
		t.Fatalf("loadRegistry(feature): %v", err)
	}
	got := reg[feature.Root]
	if len(got) != 1 || got[0] != ".env" {
		t.Errorf("registry[secondary.Root] = %v, want [.env]", got)
	}

	// (3) No entry keyed by the primary's Root — the primary
	// workspace was not detached, so it MUST NOT appear as a key.
	// This is the "per-workspace isolation" invariant expressed as a
	// property of the KEYS, which is what actually protects primary
	// from being falsely reported as detached by `wrk status`.
	if primaryEntry, ok := reg[primary.Root]; ok {
		t.Errorf("registry has entry keyed by primary.Root = %v, want none", primaryEntry)
	}

	// (4) Loaded from the primary's Repository (same file, same
	// bytes), the primary's Root still has no entry. This is the
	// direct expression of "primary's view of the registry does not
	// list feature's paths under primary.Root".
	regFromPrimary, err := loadRegistry(primary)
	if err != nil {
		t.Fatalf("loadRegistry(primary): %v", err)
	}
	if primaryEntry, ok := regFromPrimary[primary.Root]; ok {
		t.Errorf("primary-view registry lists primary.Root = %v, want no entry",
			primaryEntry)
	}
}

// TestLinkOnPrimaryDoesNotAffectSecondaryRegistry pins the flip
// side of the isolation invariant: Link on one workspace clears
// ONLY that workspace's registry entry (clearDetached deletes by
// repo.Root). A regression that made Link clear the whole file, or
// that keyed clearDetached against a stale/global path, would let
// a plain `wrk link` on the primary silently wipe every worktree's
// intentional detach record — turning intentional divergence back
// into false conflicts in the next `wrk status` output.
func TestLinkOnPrimaryDoesNotAffectSecondaryRegistry(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	_, feature := addGitWorktree(t, primary, "feature")

	storage := storageIn(t, primary.Root)

	// Secondary path only, primary path absent — see the sibling
	// test for the rationale.
	writeFile(t, filepath.Join(feature.Root, ".env"), "secondary=1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(feature, opts); err != nil {
		t.Fatalf("Link(feature): %v", err)
	}
	if err := Detach(feature, opts); err != nil {
		t.Fatalf("Detach(feature): %v", err)
	}

	// Snapshot the secondary's entry so a survival check is precise.
	before, err := loadRegistry(feature)
	if err != nil {
		t.Fatalf("loadRegistry pre-primary-Link: %v", err)
	}
	beforeEntry := append([]string(nil), before[feature.Root]...)
	if len(beforeEntry) == 0 {
		t.Fatalf("secondary registry seed is empty; setup broken")
	}

	// Link on the primary. Primary/.env doesn't exist, but shared
	// storage already has bytes from the secondary's earlier Link, so
	// the plan is a single Symlink into shared. Link succeeds and
	// calls clearDetached(primary), which MUST touch only the
	// primary.Root key.
	if err := Link(primary, opts); err != nil {
		t.Fatalf("Link(primary): %v", err)
	}

	after, err := loadRegistry(feature)
	if err != nil {
		t.Fatalf("loadRegistry post-primary-Link: %v", err)
	}

	// (1) Primary's entry — empty before, MUST still be empty. This
	// is trivially true (clearDetached is a no-op when the key is
	// absent) but a regression that stored an empty slice under the
	// key would change the map shape and be caught here.
	if primaryEntry, ok := after[primary.Root]; ok {
		t.Errorf("primary.Root key appeared after Link(primary) = %v, want no entry",
			primaryEntry)
	}

	// (2) Secondary's entry survives with the exact same paths.
	afterEntry := after[feature.Root]
	if len(afterEntry) != len(beforeEntry) {
		t.Fatalf("secondary registry len = %d, want %d (before=%v, after=%v)",
			len(afterEntry), len(beforeEntry), beforeEntry, afterEntry)
	}
	for i, want := range beforeEntry {
		if afterEntry[i] != want {
			t.Errorf("registry[secondary][%d] = %q, want %q (Link(primary) mutated it)",
				i, afterEntry[i], want)
		}
	}
}

// hasRegistryFile walks root looking for a file named `detached.json`
// under a `wrk/` parent directory. Used to verify the registry file
// exists under a given MetadataDir without hardcoding its subpath —
// so the assertion survives a future move of the registry inside
// MetadataDir (as long as the parent naming convention holds).
func hasRegistryFile(t *testing.T, root string) bool {
	t.Helper()
	var found bool
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == "detached.json" &&
			filepath.Base(filepath.Dir(path)) == "wrk" {
			found = true
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil && !found {
		// The registry directory might not exist yet — that's a
		// negative answer, not a walk error we should surface.
		if !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return found
}
