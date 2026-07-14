package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
		return nil, Wrapf(ErrConfigInvalid,
			"check .wrk.yml for syntax errors or invalid resource paths",
			err, "%s", err.Error())
	}

	var variants []variant

	for _, resource := range cfg.Resources {
		instances, err := resolver.ResolveWithStorage(repo.Root, storageRepoRoot(repo, options), resource)
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
				// Isolated variants (isolated-<hex>/) are deliberately
				// enumerated: excluding them here would hide them from
				// the keep/delete accounting. Their protection is the
				// isolation pin in pinnedVariantsForRoots, not
				// invisibility.
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
// Pins are discovered from the VARIANT side, not from configs: each
// scanned variant knows its repo-relative resource path, and the walk
// probes exactly Join(root, v.Path) in every workspace. Configs
// legitimately diverge across worktrees (branches rename or drop
// resources, .wrk.local.yml overlays differ) — resolving the invoking
// workspace's config against every sibling used to blind the sweep to
// pins at paths that sibling's own config places elsewhere, deleting
// variants a live workspace was symlinked to. The variant-side probe
// cannot drift: if the symlink exists at the variant's own path, it
// pins, no matter what any config says today.
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

	// Canonicalize each variant base once (EvalSymlinks resolves
	// /var → /private/var on macOS); the per-root probe resolves the
	// workspace side the same way so isPathInside compares like forms.
	canonBases := make([]string, len(variants))
	for i, v := range variants {
		if base, err := filepath.EvalSymlinks(v.StoragePath); err == nil {
			canonBases[i] = base
		} else {
			canonBases[i] = v.StoragePath
		}
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

		for i, v := range variants {
			if pinned[v.StoragePath] {
				continue
			}
			if workspacePinsPath(workspaceRoot, v.Path, canonBases[i]) {
				pinned[v.StoragePath] = true
			}
		}
	}

	// Pin isolation targets too. The per-resource symlink walk above
	// already catches an isolated variant whose workspace symlink
	// resolves to it, but the isolation registry is the authoritative
	// pin: if a user temporarily removes the workspace symlink (say,
	// to inspect state) or a partial isolate left the filesystem out
	// of sync, the registry still tells us the variant is claimed.
	// Losing an isolated variant to gc would silently destroy a
	// workspace's private state — belt-and-suspenders here is cheap.
	//
	// EXCEPT entries whose workspace root is gone: those are swept by
	// this same gc run (see OrphanedIsolationEntries), and pinning
	// them would make the variant survive until a second run — "I
	// gc'd, why is it still there?" Only a definite os.IsNotExist
	// skips the pin; any other stat error (permission, transient)
	// pins conservatively, matching the unreachable-workspace policy
	// above.
	reg, err := loadIsolation(repo)
	if err != nil {
		return pinned, unreachable, err
	}
	for wsRoot, entries := range reg {
		if _, statErr := os.Stat(wsRoot); os.IsNotExist(statErr) {
			continue // orphaned — swept this run, don't pin
		}
		for _, entry := range entries {
			pinned[entry.StoragePath] = true
		}
	}

	return pinned, unreachable, nil
}

// workspacePinsPath reports whether root's copy of the repo-relative
// resource path is a symlink resolving into canonBase (a variant's
// canonicalized storage path). Anything that is not a resolvable
// symlink into the base — a real directory (detached), a dangling
// link, a user symlink pointing elsewhere — is not a pin.
//
// Shared by the plan-time pin walk (pinnedVariantsForRoots) and the
// execute-time re-check (variantStillPinned) so the two can never
// disagree about what constitutes a pin.
func workspacePinsPath(root, relPath, canonBase string) bool {
	wsResource := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Lstat(wsResource)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(wsResource)
	if err != nil {
		return false
	}
	return isPathInside(canonBase, resolved)
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
	// PendingSwaps carries mid-swap-crash recoveries from `wrk run`'s
	// force-reprovision path (see internal/executor/execute.go
	// runInitialize). Fingerprint: a `.wrk-provisioning/` exists AND
	// its sibling `.wrk-deleting/` exists AND the real variant is
	// missing. That triple is only reachable when the process died
	// between the swap-aside `Rename(real, deleting)` and the
	// install `Rename(tmp, real)`. A successful hook run's output
	// is already on disk under `.wrk-provisioning/` — plain sweep
	// would throw it away and force `wrk link` to re-run the hook
	// (external side effects). The executor promotes each swap
	// before the standard sweep and lets the sibling `.wrk-deleting`
	// path fall through StaleDeleting normally.
	PendingSwaps []pendingSwap
	// OrphanedIsolationEntries names (workspaceRoot, resourcePath)
	// pairs in `isolated.json` whose workspaceRoot no longer exists
	// on disk. Storage under those entries is already caught by the
	// ghost-workspace variant sweep; clearing the registry entry
	// keeps the JSON file from accreting forever as workspaces come
	// and go.
	OrphanedIsolationEntries []orphanedIsolation
}

// pendingSwap is one mid-swap-crash recovery record. Both paths are
// absolute. The executor performs `Rename(Provisioning, Real)` and
// then leaves the sibling `.wrk-deleting/` to the standard sweep.
type pendingSwap struct {
	Provisioning string // <variant>.wrk-provisioning
	Real         string // <variant> — target of the recovery rename
}

// orphanedIsolation is one registry entry the executor will clear
// via clearIsolation. Order-independent, sorted by (Workspace,
// Resource) at detection time so tests are deterministic.
type orphanedIsolation struct {
	WorkspaceRoot string
	ResourcePath  string
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

	// Isolation-registry orphan sweep — happens BEFORE the storage
	// tree walk so a repo with no shared storage yet (fresh Link
	// never run) still gets its registry pruned when a workspace
	// gets rm -rf'd.
	detectOrphanedIsolation(repo, &result)

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
			// Distinguish mid-swap-crash (recoverable) from stale
			// scratch (sweepable). Only when BOTH the sibling
			// `.wrk-deleting/` is present AND the real variant is
			// missing has runInitialize crashed between its two
			// renames — the hook already ran, its output sits under
			// `.wrk-provisioning/`, and promoting it back to real
			// avoids re-running the hook (external side effects). If
			// the real variant is present, the crash didn't happen
			// there — sweep as stale. If the deleting sibling is
			// missing, the swap-aside never started — sweep as stale.
			variantPath := strings.TrimSuffix(path, ".wrk-provisioning")
			deletingPath := variantPath + ".wrk-deleting"
			_, realStatErr := os.Stat(variantPath)
			_, deletingStatErr := os.Stat(deletingPath)
			realMissing := os.IsNotExist(realStatErr)
			deletingExists := deletingStatErr == nil
			if realMissing && deletingExists {
				result.PendingSwaps = append(result.PendingSwaps, pendingSwap{
					Provisioning: path,
					Real:         variantPath,
				})
				return fs.SkipDir
			}
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

// detectOrphanedIsolation is the read-only isolation-registry sweep
// for cleanBookkeepingDetect. It appends to result.OrphanedIsolationEntries
// every (workspaceRoot, resourcePath) pair whose workspaceRoot is gone
// from disk and returns nothing. Errors from loadIsolation are
// swallowed silently — the corrupt-tolerant load path already logs to
// stderr, and gc's detect pass is best-effort by convention.
//
// Sorted output on (WorkspaceRoot, ResourcePath) so plan rendering
// and tests stay stable across registry map iteration.
func detectOrphanedIsolation(repo *repository.Repository, result *bookkeepingCleanup) {
	iso, err := loadIsolation(repo)
	if err != nil {
		return
	}
	for wsRoot, entries := range iso {
		if _, statErr := os.Stat(wsRoot); !os.IsNotExist(statErr) {
			continue
		}
		for resourcePath := range entries {
			result.OrphanedIsolationEntries = append(result.OrphanedIsolationEntries, orphanedIsolation{
				WorkspaceRoot: wsRoot,
				ResourcePath:  resourcePath,
			})
		}
	}
	sort.Slice(result.OrphanedIsolationEntries, func(i, j int) bool {
		a, b := result.OrphanedIsolationEntries[i], result.OrphanedIsolationEntries[j]
		if a.WorkspaceRoot != b.WorkspaceRoot {
			return a.WorkspaceRoot < b.WorkspaceRoot
		}
		return a.ResourcePath < b.ResourcePath
	})
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

	// PendingSwaps carries mid-swap-crash recoveries the executor
	// runs BEFORE the standard sweep. See bookkeepingCleanup for the
	// crash fingerprint. Empty for the vast majority of gc runs.
	PendingSwaps []pendingSwap

	// OrphanedIsolationEntries names isolation-registry pairs whose
	// workspaceRoot is gone. The executor clears each via
	// clearIsolation, sequentially, under the shared registry flock.
	OrphanedIsolationEntries []orphanedIsolation

	// OrphanedStorage lists storage subtrees no live workspace's
	// configuration claims — leftovers of resources removed or
	// renamed in .wrk.yml. The executor deletes each under the same
	// lock/re-check/rename-then-remove discipline as variants.
	OrphanedStorage []orphanedTree

	// OrphanedStorageNotes explains a skipped orphaned-storage sweep
	// (unreadable config in some workspace, unreadable isolation
	// registry). Informational; never counts toward HasNothing.
	OrphanedStorageNotes []string

	// TotalBytesFreed is the sum of DeleteVariants[*].Size plus
	// OrphanedStorage[*].Size.
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
		len(p.StaleForgetting) == 0 &&
		len(p.PendingSwaps) == 0 &&
		len(p.OrphanedIsolationEntries) == 0 &&
		len(p.OrphanedStorage) == 0
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

	// Cross-clone awareness: self-register (so OTHER clones' gc sees
	// this one) and fold every other registered clone's live roots
	// into the pin walk and the orphan sweep's config union. An
	// unenumerable clone forces full conservatism below.
	registerClone(repo, options)
	cloneRoots, unreachableClones := otherCloneRoots(repo, options)
	liveRoots = append(liveRoots, cloneRoots...)

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
	plan.UnreachableWorkspaces = append(unreachable, unreachableClones...)

	// A clone whose workspaces could not be enumerated may pin any
	// variant — keep everything, exactly like an unreachable sibling
	// workspace.
	if len(unreachableClones) > 0 {
		for _, v := range variants {
			pinned[v.StoragePath] = true
		}
	}

	bookkeeping, err := cleanBookkeepingDetect(repo, options)
	if err != nil {
		return GCPlan{}, err
	}
	plan.OrphanedLocks = bookkeeping.OrphanedLocks
	plan.StaleProvisioning = bookkeeping.StaleProvisioning
	plan.StaleDeleting = bookkeeping.StaleDeleting
	plan.StaleForgetting = bookkeeping.StaleForgetting
	plan.PendingSwaps = bookkeeping.PendingSwaps
	plan.OrphanedIsolationEntries = bookkeeping.OrphanedIsolationEntries

	for _, v := range variants {
		if pinned[v.StoragePath] {
			plan.KeepVariants = append(plan.KeepVariants, v)
			continue
		}
		plan.DeleteVariants = append(plan.DeleteVariants, v)
		plan.TotalBytesFreed += v.Size
	}

	// Orphaned-storage sweep: unclaimed subtrees under this repo's
	// storage. Skipped (with a note) when any workspace OR clone was
	// unreachable — the config union would be incomplete, and
	// deleting storage on a partial view risks data loss.
	switch {
	case len(unreachable) > 0:
		plan.OrphanedStorageNotes = append(plan.OrphanedStorageNotes,
			"orphaned-storage sweep skipped: some workspaces were unreachable")
	case len(unreachableClones) > 0:
		plan.OrphanedStorageNotes = append(plan.OrphanedStorageNotes,
			"orphaned-storage sweep skipped: a clone sharing this storage could not be enumerated")
	default:
		orphanTrees, notes, err := detectOrphanedStorage(repo, options, liveRoots)
		if err != nil {
			return GCPlan{}, err
		}
		plan.OrphanedStorageNotes = append(plan.OrphanedStorageNotes, notes...)
		plan.OrphanedStorage = orphanTrees
		for _, t := range orphanTrees {
			plan.TotalBytesFreed += t.Size
		}
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
