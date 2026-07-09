package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// variant describes one shared copy of a resource sitting on disk. For
// fingerprinted resources the Fingerprint field holds the subdir name;
// for un-fingerprinted resources Fingerprint is empty and StoragePath
// points directly at the resource dir.
type variant struct {
	Resource    string    // config resource name (e.g. "node")
	Path        string    // resource repo-relative path (e.g. "node_modules")
	Fingerprint string    // subdir name; "" for un-fingerprinted resources
	StoragePath string    // absolute path to the variant on disk
	Size        int64     // bytes; walks the subtree via treeSize
	LastUsed    time.Time // subdir mtime (Stat().ModTime())
}

// scanVariants enumerates every fingerprint variant currently present
// on disk for every configured resource in repo. Un-fingerprinted
// resources produce at most one variant with an empty Fingerprint. The
// function is read-only; it never writes to storage.
//
// Resources whose shared subtree does not exist yet contribute zero
// variants; scanVariants does NOT treat that as an error.
func scanVariants(repo *repository.Repository, options Options) ([]variant, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, err
	}

	var variants []variant

	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(repo.Root, resource)
		if err != nil {
			return nil, err
		}

		for _, instance := range instances {
			fingerprinted := len(instance.FingerprintInputs) > 0
			// For fingerprinted resources this is the parent that holds
			// each variant subdir; for un-fingerprinted resources it is
			// the single shared copy itself.
			subtree := filepath.Join(
				options.StorageRoot,
				repo.RepositoryID,
				instance.RelativePath,
			)

			if !fingerprinted {
				// Un-fingerprinted: at most one variant, keyed by
				// existence of the shared path itself. Missing storage
				// is not an error — it just means Link has never run.
				if _, err := os.Lstat(subtree); err != nil {
					continue
				}
				variants = append(variants, newVariant(
					instance.Resource.Name,
					instance.RelativePath,
					"",
					subtree,
				))
				continue
			}

			entries, err := os.ReadDir(subtree)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			for _, entry := range entries {
				name := entry.Name()
				if isBookkeeping(name) {
					continue
				}
				if !entry.IsDir() {
					continue
				}
				variants = append(variants, newVariant(
					instance.Resource.Name,
					instance.RelativePath,
					name,
					filepath.Join(subtree, name),
				))
			}
		}
	}

	return variants, nil
}

// newVariant populates a variant with best-effort Size and LastUsed.
// treeSize/Stat failures don't abort the read-only sweep — a variant
// with Size=0 or LastUsed=zero is still more useful to the plan builder
// than a whole aborted scan.
func newVariant(resource, path, fingerprint, storagePath string) variant {
	v := variant{
		Resource:    resource,
		Path:        path,
		Fingerprint: fingerprint,
		StoragePath: storagePath,
	}
	if size, err := treeSize(storagePath); err == nil {
		v.Size = size
	}
	if info, err := os.Stat(storagePath); err == nil {
		v.LastUsed = info.ModTime()
	}
	return v
}

// pinnedVariants returns the set of variant storage paths (absolute)
// currently symlinked from any workspace of repo. Convenience wrapper
// over pinnedVariantsForRoots that uses the unfiltered result of
// repo.Workspaces(); callers that need to exclude ghosts (e.g.
// BuildGCPlan) must call pinnedVariantsForRoots directly.
//
// See pinnedVariantsForRoots for the unreachable-workspace semantics.
func pinnedVariants(
	repo *repository.Repository,
	options Options,
) (map[string]bool, []string, error) {
	workspaces, err := repo.Workspaces()
	if err != nil {
		return nil, nil, err
	}
	return pinnedVariantsForRoots(repo, options, workspaces)
}

// pinnedVariantsForRoots is the ghost-aware core of the pin walk. It
// returns the set of variant storage paths (absolute) currently
// symlinked from any workspace root in roots. Unreachable roots (Stat
// failure on the root itself) are treated conservatively — every
// scanned variant is marked pinned to avoid deleting data referenced
// from a workspace we cannot inspect. The unreachable roots are
// returned so the plan builder can surface why the sweep was
// conservative.
//
// Non-managed symlinks (targets outside any scanned variant) do not
// appear in the pinned set.
//
// BuildGCPlan calls this with liveRoots (Workspaces() MINUS ghosts):
// a ghost's working dir may be gone entirely, which would otherwise
// hit the unreachable branch and conservatively pin every variant —
// wrong for gc, because we know that workspace is going away.
func pinnedVariantsForRoots(
	repo *repository.Repository,
	options Options,
	roots []string,
) (map[string]bool, []string, error) {
	variants, err := scanVariants(repo, options)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, nil, err
	}

	pinned := make(map[string]bool)
	var unreachable []string

	pinAll := func() {
		for _, v := range variants {
			pinned[v.StoragePath] = true
		}
	}

	for _, workspaceRoot := range roots {
		if _, err := os.Stat(workspaceRoot); err != nil {
			unreachable = append(unreachable, workspaceRoot)
			pinAll()
			continue
		}

		for _, resource := range cfg.Resources {
			// Use the workspace's OWN root so {root}-anchored globs and
			// paths expand against the workspace under inspection.
			instances, err := resolver.Resolve(workspaceRoot, resource)
			if err != nil {
				return nil, nil, err
			}

			for _, instance := range instances {
				info, err := os.Lstat(instance.WorkspacePath)
				if err != nil {
					continue
				}
				if info.Mode()&os.ModeSymlink == 0 {
					continue
				}

				resolved, err := filepath.EvalSymlinks(instance.WorkspacePath)
				if err != nil {
					continue
				}

				for _, v := range variants {
					// EvalSymlinks canonicalizes the resolved target
					// (e.g. `/private/var/...` on macOS); do the same to
					// the base so isPathInside compares equivalent forms.
					base, err := filepath.EvalSymlinks(v.StoragePath)
					if err != nil {
						base = v.StoragePath
					}
					if isPathInside(base, resolved) {
						pinned[v.StoragePath] = true
						break
					}
				}
			}
		}
	}

	return pinned, unreachable, nil
}

// isPathInside reports whether target is base or lives inside base.
// Both must be absolute paths; filepath.Rel keeps the check honest
// across differing but equivalent spellings.
func isPathInside(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// bookkeepingCleanup describes stale files/dirs that gc's executor
// should remove. All entries are absolute paths.
type bookkeepingCleanup struct {
	OrphanedLocks     []string // <variant>.wrk-lock files whose variant is gone
	StaleProvisioning []string // <variant>.wrk-provisioning/ dirs whose flock is NOT held
	StaleDeleting     []string // <variant>.wrk-deleting/ dirs left by a crashed gc
	// StaleForgetting is populated when a prior `wrk forget` crashed
	// between the rename and the RemoveAll. It sits as a sibling of
	// the repo-id subtree, not inside it, so cleanBookkeepingDetect
	// checks for it after the tree walk.
	StaleForgetting []string
}

// cleanBookkeepingDetect walks the shared-storage tree of repo and
// returns absolute paths of cruft the executor is allowed to remove.
// Read-only: nothing on disk is modified. Locks are probed non-
// blockingly and released immediately; a held lock leaves the
// associated .wrk-provisioning out of the returned list.
func cleanBookkeepingDetect(repo *repository.Repository, options Options) (bookkeepingCleanup, error) {
	var result bookkeepingCleanup

	// Sibling check first: a `.wrk-forgetting` marker at the storage
	// root indicates a crashed `wrk forget`. It's outside the
	// per-repo walk below because that walk keys on repo.RepositoryID
	// (which the marker filename encodes rather than nests under).
	marker := filepath.Join(options.StorageRoot, repo.RepositoryID+".wrk-forgetting")
	if _, err := os.Stat(marker); err == nil {
		result.StaleForgetting = append(result.StaleForgetting, marker)
	}

	root := filepath.Join(options.StorageRoot, repo.RepositoryID)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Best-effort: a transient error on one entry shouldn't
			// abort the sweep of the rest of the tree.
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()

		switch {
		case strings.HasSuffix(name, ".wrk-provisioning"):
			result.StaleProvisioning = appendIfStaleProvisioning(result.StaleProvisioning, path)
			return fs.SkipDir

		case strings.HasSuffix(name, ".wrk-deleting"):
			result.StaleDeleting = append(result.StaleDeleting, path)
			return fs.SkipDir

		case strings.HasSuffix(name, ".wrk-lock"):
			variantPath := strings.TrimSuffix(path, ".wrk-lock")
			if _, err := os.Stat(variantPath); os.IsNotExist(err) {
				result.OrphanedLocks = append(result.OrphanedLocks, path)
			}
			if d.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	if walkErr != nil {
		return result, walkErr
	}
	return result, nil
}

// appendIfStaleProvisioning probes the sibling <variant>.wrk-lock non-
// blockingly. A missing lock file means nobody could possibly hold it,
// so the provisioning dir is stale. A held lock means a peer is
// actively provisioning and we leave it alone. Any other stat/lock
// error is treated conservatively (skip).
func appendIfStaleProvisioning(dst []string, provPath string) []string {
	lockPath := strings.TrimSuffix(provPath, ".wrk-provisioning") + ".wrk-lock"

	if _, err := os.Stat(lockPath); err != nil {
		if os.IsNotExist(err) {
			return append(dst, provPath)
		}
		return dst
	}

	l := flock.New(lockPath)
	ok, err := l.TryLock()
	if err != nil || !ok {
		return dst
	}
	_ = l.Unlock()
	return append(dst, provPath)
}

// GCPlan is the composed, read-only result of the detection sweeps
// run by BuildGCPlan. `wrk gc` prints it for the user to confirm and
// then hands it to the executor (Task 1.7) which performs the
// mutations. Nothing in this struct implies work has already been
// done — every field is a "would-do" list.
type GCPlan struct {
	// Ghosts lists workspace roots the VCS still remembers but whose
	// working directory is gone. The executor will prune them.
	Ghosts []string

	// OrphanRegistry lists detach-registry keys whose workspace root
	// is no longer live (after ghost removal). The executor will
	// clear them.
	OrphanRegistry []string

	// KeepVariants is the set of variants currently symlinked from a
	// live workspace and therefore preserved by the sweep.
	KeepVariants []variant

	// DeleteVariants is the set of variants no live workspace
	// references. The executor will remove them.
	DeleteVariants []variant

	// OrphanedLocks lists .wrk-lock files whose sibling variant has
	// already been removed and are therefore safe to sweep.
	OrphanedLocks []string

	// StaleProvisioning lists .wrk-provisioning dirs whose sibling
	// lock is NOT held by any live process.
	StaleProvisioning []string

	// StaleDeleting lists .wrk-deleting markers left behind by a
	// crashed prior gc; always safe to sweep.
	StaleDeleting []string

	// StaleForgetting lists <repo-id>.wrk-forgetting markers left
	// behind by a crashed prior `wrk forget`; always safe to sweep.
	StaleForgetting []string

	// UnreachableWorkspaces lists workspaces the pin walk could not
	// stat. For each, pinnedVariantsForRoots conservatively pinned
	// every scanned variant; surfaced here so the CLI can explain
	// why DeleteVariants may be smaller than the user expects.
	UnreachableWorkspaces []string

	// TotalBytesFreed is the sum of DeleteVariants[*].Size.
	TotalBytesFreed int64
}

// HasNothing reports whether executing the plan would touch any state.
// UnreachableWorkspaces and KeepVariants are informational only, so
// they do not count. `wrk gc` uses this to skip the confirmation prompt
// when there is nothing to do.
func (p GCPlan) HasNothing() bool {
	return len(p.Ghosts) == 0 &&
		len(p.OrphanRegistry) == 0 &&
		len(p.DeleteVariants) == 0 &&
		len(p.OrphanedLocks) == 0 &&
		len(p.StaleProvisioning) == 0 &&
		len(p.StaleDeleting) == 0 &&
		len(p.StaleForgetting) == 0
}

// BuildGCPlan runs the read-only detection sweeps and composes them
// into a single plan. Nothing on disk, in the VCS, or in the detach
// registry is modified — the returned plan is a preview.
//
// Sweep order is significant:
//
//  1. DetectGhosts enumerates workspaces the VCS remembers but whose
//     working directory is gone. It runs first so the remaining
//     sweeps can filter ghosts out of "live" workspaces.
//  2. liveRoots = repo.Workspaces() MINUS Ghosts. Every subsequent
//     sweep that needs the live set uses this filtered slice.
//  3. detectOrphanRegistryEntries flags registry keys whose workspace
//     is not in liveRoots (i.e., a ghost or a truly-unknown root).
//  4. scanVariants enumerates the on-disk variants.
//  5. pinnedVariantsForRoots walks liveRoots (NOT Workspaces()) so
//     variants referenced only by a ghost are not spuriously kept.
//     A ghost's working dir is missing, which would otherwise trip
//     the unreachable branch and pin everything. We filter it out
//     up front to keep unreachable-workspace warnings honest.
//  6. cleanBookkeepingDetect finds stale locks/provisioning/deleting.
//  7. Variants split into KeepVariants (pinned or unreachable-pinned)
//     and DeleteVariants (everything else); DeleteVariants.Size sums
//     into TotalBytesFreed.
func BuildGCPlan(repo *repository.Repository, options Options) (GCPlan, error) {
	var plan GCPlan

	ghosts, err := repo.DetectGhosts()
	if err != nil {
		return GCPlan{}, err
	}
	plan.Ghosts = ghosts

	workspaces, err := repo.Workspaces()
	if err != nil {
		return GCPlan{}, err
	}
	liveRoots := filterOutGhosts(workspaces, ghosts)

	orphans, err := detectOrphanRegistryEntries(repo, liveRoots)
	if err != nil {
		return GCPlan{}, err
	}
	plan.OrphanRegistry = orphans

	variants, err := scanVariants(repo, options)
	if err != nil {
		return GCPlan{}, err
	}

	pinned, unreachable, err := pinnedVariantsForRoots(repo, options, liveRoots)
	if err != nil {
		return GCPlan{}, err
	}
	plan.UnreachableWorkspaces = unreachable

	bookkeeping, err := cleanBookkeepingDetect(repo, options)
	if err != nil {
		return GCPlan{}, err
	}
	plan.OrphanedLocks = bookkeeping.OrphanedLocks
	plan.StaleProvisioning = bookkeeping.StaleProvisioning
	plan.StaleDeleting = bookkeeping.StaleDeleting
	plan.StaleForgetting = bookkeeping.StaleForgetting

	for _, v := range variants {
		if pinned[v.StoragePath] {
			plan.KeepVariants = append(plan.KeepVariants, v)
			continue
		}
		plan.DeleteVariants = append(plan.DeleteVariants, v)
		plan.TotalBytesFreed += v.Size
	}

	return plan, nil
}

// filterOutGhosts returns roots with any entry in ghosts removed.
// Called only with the modest slices from repo.Workspaces() and
// repo.DetectGhosts(), so a linear scan per ghost is fine.
func filterOutGhosts(roots, ghosts []string) []string {
	if len(ghosts) == 0 {
		return roots
	}
	skip := make(map[string]bool, len(ghosts))
	for _, g := range ghosts {
		skip[g] = true
	}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if skip[r] {
			continue
		}
		out = append(out, r)
	}
	return out
}
