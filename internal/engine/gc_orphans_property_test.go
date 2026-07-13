package engine

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestGCOrphanClassificationProperty drives detectOrphanedStorage with
// randomly drawn protected relpath sets (written as real resources in
// .wrk.yml) and randomly drawn storage relpaths (materialized as real
// directory chains on disk), and asserts the classification invariants
// that make the sweep safe:
//
//  1. SOUNDNESS — no orphan equals a protected path, is an ancestor of
//     one (those are kept-but-descended), or descends from one (a
//     protected path's interior belongs to the variant sweep). Any
//     violation is the "gc deleted configured storage" data-loss bug.
//  2. COMPLETENESS — every on-disk path (including every implied
//     intermediate directory) that is unrelated to every protected
//     path is covered by exactly the orphan set: it is an orphan root
//     itself or sits inside one. Silently kept garbage would accrete
//     forever.
//  3. MAXIMALITY + DETERMINISM — orphans are disjoint subtree roots
//     (never nested in each other), sorted by RelPath, and each names
//     a real on-disk entry with StoragePath = <storage>/<repo-id>/<rel>.
//  4. Bookkeeping entries are never orphaned; a clean sweep reports no
//     notes and no error.
//
// Only directories are drawn (MkdirAll chains), so the walk's
// dir-only descent through ancestors is always applicable. rapid.TB
// has no TempDir/Setenv, so the outer *testing.T is captured for
// fixture helpers; property failures go through *rapid.T for seed
// reporting and shrinking.
func TestGCOrphanClassificationProperty(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storageBase := t.TempDir()

	// relPath draws a slash-joined path of 1-3 segments, each 1-8
	// lowercase letters. The alphabet excludes dots (no bookkeeping
	// suffixes, no YAML-hostile values) and every drawn value passes
	// config validation.
	segment := rapid.StringMatching(`[a-z]{1,8}`)
	relPath := rapid.Custom(func(rt *rapid.T) string {
		depth := rapid.IntRange(1, 3).Draw(rt, "depth")
		segs := make([]string, depth)
		for i := range segs {
			segs[i] = segment.Draw(rt, "seg")
		}
		return path.Join(segs...)
	})
	identity := func(s string) string { return s }

	rapid.Check(t, func(rt *rapid.T) {
		protected := rapid.SliceOfNDistinct(relPath, 1, 4, identity).Draw(rt, "protected")
		storagePaths := rapid.SliceOfNDistinct(relPath, 1, 8, identity).Draw(rt, "storagePaths")

		// Configure the protected set as real resources.
		var cfg strings.Builder
		cfg.WriteString("resources:\n")
		for i, p := range protected {
			fmt.Fprintf(&cfg, "  - name: r%d\n    path: %q\n", i, p)
		}
		writeFile(t, filepath.Join(repo.Root, ".wrk.yml"), cfg.String())

		// Materialize the storage tree in a per-iteration root.
		storageRoot, err := os.MkdirTemp(storageBase, "it")
		if err != nil {
			rt.Fatalf("MkdirTemp: %v", err)
		}
		storageRepo := filepath.Join(storageRoot, repo.RepositoryID)
		for _, s := range storagePaths {
			if err := os.MkdirAll(filepath.Join(storageRepo, filepath.FromSlash(s)), 0o755); err != nil {
				rt.Fatalf("MkdirAll(%s): %v", s, err)
			}
		}
		// Bookkeeping seeds: never orphans, whatever else is drawn.
		writeFile(t, filepath.Join(storageRepo, "scratch.wrk-lock"), "")
		if err := os.MkdirAll(filepath.Join(storageRepo, "gone.wrk-deleting"), 0o755); err != nil {
			rt.Fatalf("MkdirAll bookkeeping: %v", err)
		}

		orphans, notes, err := detectOrphanedStorage(
			repo, Options{StorageRoot: storageRoot}, []string{repo.Root})
		if err != nil {
			rt.Fatalf("detectOrphanedStorage: %v", err)
		}
		if len(notes) != 0 {
			rt.Fatalf("notes = %v, want none on a clean sweep", notes)
		}

		// created = every drawn path plus every implied intermediate
		// directory — the full set of on-disk relpaths.
		created := make(map[string]bool)
		for _, s := range storagePaths {
			for q := s; q != "."; q = path.Dir(q) {
				created[q] = true
			}
		}

		got := make([]string, len(orphans))
		for i, o := range orphans {
			got[i] = o.RelPath
		}

		if !slices.IsSorted(got) {
			rt.Fatalf("orphans not sorted by RelPath: %v", got)
		}

		for i, o := range orphans {
			if strings.HasSuffix(o.RelPath, ".wrk-lock") || strings.HasSuffix(o.RelPath, ".wrk-deleting") {
				rt.Fatalf("bookkeeping entry orphaned: %q", o.RelPath)
			}
			if !created[o.RelPath] {
				rt.Fatalf("orphan %q does not name a created on-disk path (created=%v)",
					o.RelPath, storagePaths)
			}
			wantAbs := filepath.Join(storageRepo, filepath.FromSlash(o.RelPath))
			if o.StoragePath != wantAbs {
				rt.Fatalf("orphan %q StoragePath = %q, want %q", o.RelPath, o.StoragePath, wantAbs)
			}
			// Maximality: orphan roots are disjoint subtrees. Sorted
			// order puts a would-be ancestor immediately before its
			// descendants, so pairwise-adjacent prefix checks suffice —
			// but check all pairs; the sets are tiny.
			for _, later := range got[i+1:] {
				if strings.HasPrefix(later, o.RelPath+"/") {
					rt.Fatalf("nested orphans: %q inside %q", later, o.RelPath)
				}
			}
			// SOUNDNESS.
			for _, p := range protected {
				if pathsRelated(o.RelPath, p) {
					rt.Fatalf("orphan %q overlaps protected %q (protected=%v)",
						o.RelPath, p, protected)
				}
			}
		}

		// COMPLETENESS: every created path unrelated to all protected
		// paths must sit at-or-under an orphan root.
		for q := range created {
			relatedToProtected := false
			for _, p := range protected {
				if pathsRelated(q, p) {
					relatedToProtected = true
					break
				}
			}
			if relatedToProtected {
				continue
			}
			covered := false
			for _, o := range got {
				if q == o || strings.HasPrefix(q, o+"/") {
					covered = true
					break
				}
			}
			if !covered {
				rt.Fatalf("unclaimed path %q not covered by any orphan (protected=%v orphans=%v)",
					q, protected, got)
			}
		}
	})
}

// pathsRelated reports whether two slash relpaths are equal or one
// contains the other (segment-boundary aware: "ab" is unrelated to
// "a").
func pathsRelated(a, b string) bool {
	return a == b ||
		strings.HasPrefix(a, b+"/") ||
		strings.HasPrefix(b, a+"/")
}
