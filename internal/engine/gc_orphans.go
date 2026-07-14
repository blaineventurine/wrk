package engine

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofrs/flock"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// orphanedTree is a storage subtree under `<storage>/<repo-id>/` that no
// live workspace's configuration claims — typically the leftovers of a
// resource that was later removed or renamed in .wrk.yml. scanVariants
// never sees these (it iterates configured resources), so without this
// sweep they accrete forever; only `wrk forget` would reclaim them.
type orphanedTree struct {
	// RelPath is the storage-repo-relative path in slash form (matches
	// resolver.ResourceInstance.RelativePath).
	RelPath string
	// StoragePath is the absolute on-disk path of the orphaned subtree.
	StoragePath string
	// Size is the best-effort byte total under StoragePath (same
	// tolerant treeSize policy as variant sizing).
	Size int64
}

// detectOrphanedStorage classifies the storage tree of repo against the
// union of every live workspace's configuration.
//
// Protection rules — a storage path is NEVER an orphan when it is:
//
//   - a configured resource path (its interior is variants/content and
//     belongs to the variant sweep, not this one),
//   - an ancestor of a configured resource path (an intermediate
//     directory like `client/` for resource `client/node_modules`),
//   - an isolation-registry storage path or an ancestor of one (an
//     isolated variant's content is not reproducible; even when its
//     resource left the config the pin keeps it alive),
//   - executor/gc bookkeeping (`*.wrk-deleting`, `*.wrk-lock`, ... —
//     owned by the bookkeeping sweep).
//
// Everything else is orphaned: no configured resource can ever link to
// it again under the current configs.
//
// The union is built from EVERY live workspace's own .wrk.yml (configs
// legitimately diverge across branches and .wrk.local.yml). Any failure
// to load or resolve a workspace's config aborts the sweep with a note
// instead of guessing — deleting storage based on a partial view of the
// configs is how data gets lost. The returned notes explain a skipped
// sweep; they are surfaced in the plan.
func detectOrphanedStorage(
	repo *repository.Repository,
	options Options,
	liveRoots []string,
) ([]orphanedTree, []string, error) {
	storageRoot := storageRepoRoot(repo, options)

	if _, err := os.Stat(storageRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	protected := make(map[string]bool)

	for _, root := range liveRoots {
		cfg, err := config.Load(root)
		if err != nil {
			return nil, []string{fmt.Sprintf(
				"orphaned-storage sweep skipped: config unreadable in %s (%v)",
				root, err,
			)}, nil
		}
		for _, resource := range cfg.Resources {
			instances, err := resolver.ResolveWithStorage(
				root, storageRoot, resource,
			)
			if err != nil {
				return nil, []string{fmt.Sprintf(
					"orphaned-storage sweep skipped: resource %q unresolvable in %s (%v)",
					resource.Name, root, err,
				)}, nil
			}
			for _, instance := range instances {
				protected[instance.RelativePath] = true
			}
			// A glob resource with zero current matches still claims
			// its pattern's future subtree; protecting the pattern's
			// static prefix (everything before the first meta
			// character) keeps e.g. `packages/` alive for
			// `packages/*/node_modules` even when no package is
			// currently provisioned.
			if prefix := globStaticPrefix(resource.Path); prefix != "" {
				protected[prefix] = true
			}
		}
	}

	// Isolation pins survive config removal: entries are keyed by
	// workspace, but their storage paths all live under this repo's
	// subtree. Protect each pinned path (ancestor logic below keeps
	// the chain alive).
	iso, err := loadIsolation(repo)
	if err != nil {
		return nil, []string{fmt.Sprintf(
			"orphaned-storage sweep skipped: isolation registry unreadable (%v)", err,
		)}, nil
	}
	absStorage, err := filepath.Abs(storageRoot)
	if err != nil {
		return nil, nil, err
	}
	for _, entries := range iso {
		for _, entry := range entries {
			rel, err := filepath.Rel(absStorage, entry.StoragePath)
			if err != nil || rel == "." || rel == ".." ||
				strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue // not under this repo's storage; irrelevant here
			}
			protected[filepath.ToSlash(rel)] = true
		}
	}

	// ancestors: every proper ancestor of a protected path, so the walk
	// can descend through intermediate directories instead of flagging
	// them.
	ancestors := make(map[string]bool)
	for p := range protected {
		for dir := path.Dir(p); dir != "." && dir != "/"; dir = path.Dir(dir) {
			ancestors[dir] = true
		}
	}

	var orphans []orphanedTree
	if err := classifyStorageDir(storageRoot, "", protected, ancestors, &orphans); err != nil {
		return nil, nil, err
	}

	sort.Slice(orphans, func(i, j int) bool {
		return orphans[i].RelPath < orphans[j].RelPath
	})
	return orphans, nil, nil
}

// classifyStorageDir walks one directory level of the storage tree,
// recursing through ancestors of protected paths and collecting
// everything unclaimed as orphans. Protected paths themselves are never
// entered — their interiors belong to the variant sweep.
func classifyStorageDir(
	dir string,
	rel string,
	protected, ancestors map[string]bool,
	orphans *[]orphanedTree,
) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Unreadable level: claim nothing rather than orphan blindly.
		return nil
	}

	for _, entry := range entries {
		name := entry.Name()
		if isBookkeeping(name) {
			continue // bookkeeping sweeps own these
		}

		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		childAbs := filepath.Join(dir, name)

		switch {
		case protected[childRel]:
			continue

		case ancestors[childRel] && entry.IsDir():
			if err := classifyStorageDir(
				childAbs, childRel, protected, ancestors, orphans,
			); err != nil {
				return err
			}

		default:
			tree := orphanedTree{
				RelPath:     childRel,
				StoragePath: childAbs,
			}
			if size, err := treeSize(childAbs); err == nil {
				tree.Size = size
			}
			*orphans = append(*orphans, tree)
		}
	}
	return nil
}

// globStaticPrefix returns the meta-character-free leading directory of
// a glob pattern in slash form ("packages" for "packages/*/node_modules"),
// or "" when the pattern has no static directory prefix.
func globStaticPrefix(pattern string) string {
	pattern = filepath.ToSlash(pattern)
	segments := strings.Split(pattern, "/")
	var static []string
	for _, segment := range segments {
		if strings.ContainsAny(segment, "*?[") {
			break
		}
		static = append(static, segment)
	}
	if len(static) == 0 || len(static) == len(segments) {
		// No static prefix, or not a glob at all (fully static
		// patterns are already protected as resource paths).
		return ""
	}
	return path.Join(static...)
}

// deleteOrphanedTree removes one orphaned subtree under the same
// discipline deleteVariant uses: a non-blocking flock on the tree's
// sibling `.wrk-lock`, an execute-time orphan re-check (the plan aged
// while the user stared at the confirm prompt — a `wrk link` or config
// edit may have re-claimed the path), and rename-then-remove so a crash
// mid-delete leaves a `.wrk-deleting` marker for the next gc.
func deleteOrphanedTree(
	repo *repository.Repository,
	tree orphanedTree,
	options Options,
	recordErr func(error),
) {
	lockPath := tree.StoragePath + ".wrk-lock"
	lock := flock.New(lockPath)

	got, err := lock.TryLock()
	if err != nil {
		recordErr(err)
		return
	}
	if !got {
		fmt.Fprintf(options.Stdout,
			"skipping %s: lock held by another process\n", tree.StoragePath)
		return
	}
	defer func() {
		_ = lock.Unlock()
		_ = os.Remove(lockPath)
	}()

	orphanStill, err := orphanedTreeStillUnclaimed(repo, options, tree)
	if err != nil {
		recordErr(fmt.Errorf(
			"orphan re-check for %s: %w (subtree kept)", tree.StoragePath, err,
		))
		return
	}
	if !orphanStill {
		fmt.Fprintf(options.Stdout,
			"skipping %s: re-claimed since the plan was built\n", tree.StoragePath)
		return
	}

	deleting := tree.StoragePath + ".wrk-deleting"

	if _, err := os.Lstat(deleting); err == nil {
		if err := executor.RemoveAllProgress(deleting, options.Progress); err != nil {
			recordErr(err)
		}
		return
	}

	if err := os.Rename(tree.StoragePath, deleting); err != nil {
		if os.IsNotExist(err) {
			return // already gone; idempotent rerun
		}
		recordErr(err)
		return
	}

	if err := executor.RemoveAllProgress(deleting, options.Progress); err != nil {
		recordErr(err)
	}
}

// orphanedTreeStillUnclaimed re-runs the orphan classification for a
// single subtree against CURRENT state: fresh workspace list, fresh
// per-workspace configs, fresh isolation registry, plus the live
// symlink pin check variantStillPinned performs. Any failure returns
// an error so the caller keeps the subtree (conservative).
func orphanedTreeStillUnclaimed(
	repo *repository.Repository,
	options Options,
	tree orphanedTree,
) (bool, error) {
	workspaces, err := repo.Workspaces()
	if err != nil {
		return false, err
	}

	cloneRoots, err := otherCloneRootsStrict(repo, options)
	if err != nil {
		return false, err // conservative — unenumerable clone keeps the tree
	}
	workspaces = append(workspaces, cloneRoots...)

	liveRoots := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if _, err := os.Stat(ws); err != nil {
			if os.IsNotExist(err) {
				continue // ghost
			}
			return false, err // unreachable — conservative
		}
		liveRoots = append(liveRoots, ws)
	}

	orphans, notes, err := detectOrphanedStorage(repo, options, liveRoots)
	if err != nil {
		return false, err
	}
	if len(notes) > 0 {
		return false, fmt.Errorf("%s", strings.Join(notes, "; "))
	}

	claimed := true
	for _, o := range orphans {
		if o.RelPath == tree.RelPath || isPathInside(o.StoragePath, tree.StoragePath) {
			claimed = false
			break
		}
	}
	if claimed {
		return false, nil
	}

	// Belt and suspenders: even an unclaimed path must not be deleted
	// while some workspace symlink resolves into it.
	pinned, err := variantStillPinned(repo, variant{
		Path:        filepath.FromSlash(tree.RelPath),
		StoragePath: tree.StoragePath,
	}, options)
	if err != nil {
		return false, err
	}
	return !pinned, nil
}
