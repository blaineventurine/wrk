package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/repository"
)

// ExecuteGC applies a plan to disk, registry, and VCS metadata. It
// assumes the caller has already prompted / passed --yes / --force,
// and that no other decision layer sits between BuildGCPlan and
// ExecuteGC. Locks are acquired non-blocking; a held variant lock is
// skipped with a warning written to options.Stdout.
//
// ExecuteGC is idempotent: re-running on an already-applied plan
// produces no error and no visible mutation. Errors are collected;
// the first error is returned, but partial-success mutations from
// earlier steps survive (this matches Executor conventions elsewhere
// in wrk).
//
// Step order:
//
//  1. Complete PendingSwaps from a crashed `wrk run` force-reprovision.
//     Runs BEFORE any sweep so a `.wrk-provisioning/` payload the
//     hook already produced is preserved: the standard sweep would
//     otherwise delete it and force wrk link to re-run the hook
//     (external side effects). Each swap is a single Rename; a
//     failure surfaces as a warning and the next gc run tries again.
//  2. PruneGhosts before any registry sweep — the registry sweep
//     needs the post-prune live-workspace set, and a variant sweep
//     that used a ghost's cached pin set would preserve dead data.
//  3. Sweep orphan detach-registry entries using a FRESH Workspaces()
//     call. Recomputing (rather than trusting plan.OrphanRegistry) is
//     defensive: re-running ExecuteGC after a partial application
//     produces the same result as running it once against a clean
//     plan.
//  4. Clear OrphanedIsolationEntries — isolation registry keys whose
//     workspace root is gone. Cheap map ops under the shared
//     registry flock; the on-disk isolated storage is caught by the
//     ghost-workspace variant sweep in step 5, so this step just
//     keeps `isolated.json` from accreting stale keys forever.
//  5. Delete variants. Each acquires <variant>.wrk-lock non-blocking;
//     a held lock is skipped with a warning (a concurrent wrk link is
//     provisioning). The delete uses rename-then-remove so a crash
//     mid-RemoveAll leaves a .wrk-deleting marker the next gc sweeps.
//  6. Sweep bookkeeping cruft (OrphanedLocks, StaleProvisioning,
//     StaleDeleting, StaleForgetting). Failures here log and continue
//     — leftover cruft is annoying but never corrupts state. The
//     `.wrk-deleting/` sibling of a step-1 swap arrives here as part
//     of StaleDeleting.
func ExecuteGC(repo *repository.Repository, plan GCPlan, options Options) error {
	var firstErr error
	recordErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	// Step 1: Complete mid-swap-crash recoveries. Order matters:
	// runs BEFORE the sweep so the `.wrk-provisioning/` payload of a
	// crashed `wrk run` is preserved rather than deleted-then-
	// reprovisioned (external hook side effects). A failed Rename is
	// idempotent-safe: the same crash-fingerprint reappears next run
	// and we retry. We do not abort ExecuteGC on a swap failure —
	// downstream sweeps still have work to do on the rest of the
	// tree.
	for _, swap := range plan.PendingSwaps {
		if err := os.Rename(swap.Provisioning, swap.Real); err != nil {
			fmt.Fprintf(options.Stdout,
				"warning: could not complete mid-swap recovery for %s: %v\n",
				swap.Real, err)
		}
	}

	// Step 2: Prune ghosts from VCS metadata. If this fails we bail
	// before any other mutation so the operator can retry from a
	// consistent state — a half-pruned registry against a live-ghost
	// git tree would be worse than doing nothing.
	if _, err := repo.PruneGhosts(); err != nil {
		return err
	}

	// Step 3: Sweep orphan registry entries against a freshly-queried
	// workspace set. Post-prune Workspaces() no longer sees ghost
	// worktrees, so any registry key it does not contain is genuinely
	// orphan.
	workspaces, err := repo.Workspaces()
	if err != nil {
		return err
	}
	if _, err := pruneOrphanRegistryEntries(repo, workspaces); err != nil {
		recordErr(err)
	}

	// Step 4: Clear orphaned isolation-registry entries. Sequential
	// under the shared registry flock (clearIsolation takes it every
	// call); the entries in plan.OrphanedIsolationEntries were
	// snapshotted at plan time, so this is a straight replay. Each
	// clear is a no-op on missing entries, so a partial-apply retry
	// is safe.
	for _, entry := range plan.OrphanedIsolationEntries {
		if err := clearIsolation(repo, entry.WorkspaceRoot, entry.ResourcePath); err != nil {
			recordErr(err)
		}
	}

	// Step 5: Delete variants. The per-iteration closure exists so
	// defer runs at loop-body exit, not function exit — otherwise a
	// long DeleteVariants slice would hold every lock until we
	// return.
	for _, v := range plan.DeleteVariants {
		deleteVariant(repo, v, options, recordErr)
	}

	// Step 6: Sweep bookkeeping cruft. Failures are surfaced but do
	// not abort the sweep — a leftover .wrk-lock is a cosmetic wart
	// the next gc will pick up.
	sweepBookkeeping(plan.OrphanedLocks, options)
	sweepBookkeeping(plan.StaleProvisioning, options)
	sweepBookkeeping(plan.StaleDeleting, options)
	sweepBookkeeping(plan.StaleForgetting, options)

	return firstErr
}

// deleteVariant applies one variant deletion under a non-blocking
// flock. See ExecuteGC step 3 for the ordering guarantees this
// depends on. A held lock is a warning, not an error: a concurrent
// wrk link is provisioning and we defer to it.
func deleteVariant(repo *repository.Repository, v variant, options Options, recordErr func(error)) {
	lockPath := v.StoragePath + ".wrk-lock"
	l := flock.New(lockPath)

	ok, err := l.TryLock()
	if err != nil {
		recordErr(err)
		return
	}
	if !ok {
		fmt.Fprintf(options.Stdout, "skipping %s: lock held by another process\n", v.StoragePath)
		return
	}
	defer func() {
		// Unlock first so the fd is released before the file is
		// unlinked. gofrs/flock unlocks via the open fd, so
		// os.Remove ordering wouldn't corrupt anything either way,
		// but this reads cleaner.
		_ = l.Unlock()
		_ = os.Remove(lockPath)
	}()

	// Re-verify the pin AFTER acquiring the lock: the plan's pin walk
	// ran before the Confirm prompt, and a `wrk link` completing in
	// that window can legitimately re-pin this variant (branch
	// switched back). Deleting a re-pinned variant would dangle a live
	// workspace symlink and destroy any user mutations in the variant.
	pinned, err := variantStillPinned(repo, v)
	if err != nil {
		// Conservative: verification failure keeps the variant.
		recordErr(fmt.Errorf("pin re-check for %s: %w (variant kept)", v.StoragePath, err))
		return
	}
	if pinned {
		fmt.Fprintf(options.Stdout,
			"skipping %s: re-pinned by a workspace since the plan was built\n",
			v.StoragePath)
		return
	}

	deletingPath := v.StoragePath + ".wrk-deleting"

	// Partial-recovery: a crash between os.Rename and os.RemoveAll
	// leaves the marker. In that case we skip the rename (there is
	// nothing left to move) and finish the RemoveAll.
	if _, err := os.Lstat(deletingPath); err == nil {
		if err := executor.RemoveAllProgress(deletingPath, options.Progress); err != nil {
			recordErr(err)
		}
		return
	}

	if err := os.Rename(v.StoragePath, deletingPath); err != nil {
		// Idempotence: variant was already deleted (e.g. re-running
		// on a partially-applied plan). Nothing to do, and the
		// defer will still sweep any leftover lock file.
		if os.IsNotExist(err) {
			return
		}
		recordErr(err)
		return
	}

	if err := executor.RemoveAllProgress(deletingPath, options.Progress); err != nil {
		recordErr(err)
	}
}

// variantStillPinned re-runs the pin check for a single variant
// against the CURRENT filesystem state. Mirrors pinnedVariantsForRoots'
// logic (Lstat → EvalSymlinks → isPathInside, plus the isolation-
// registry pin) but scoped to one variant so the recheck is
// O(workspaces), not O(workspaces × variants).
//
// Edge policy, matching pinnedVariantsForRoots:
//   - unreadable isolation registry / Workspaces() failure → pinned
//     (conservative: never delete on unverifiable state)
//   - workspace root Stat fails with IsNotExist → not a pin (ghost;
//     already pruned by ExecuteGC step 2)
//   - workspace root Stat fails otherwise → pinned (unreachable —
//     mirrors the unreachable-workspace semantics of the plan walk)
//   - resource path is not a symlink / missing → not a pin from
//     that workspace
func variantStillPinned(repo *repository.Repository, v variant) (bool, error) {
	// Fresh isolation check — one map lookup per workspace entry. An
	// isolate cannot re-pin a fingerprint variant today, but the
	// recheck is cheap and makes the invariant airtight.
	iso, err := loadIsolation(repo)
	if err != nil {
		return true, err // conservative: unreadable registry keeps the variant
	}
	for _, entries := range iso {
		for _, entry := range entries {
			if entry.StoragePath == v.StoragePath {
				return true, nil
			}
		}
	}

	workspaces, err := repo.Workspaces()
	if err != nil {
		return true, err // conservative
	}
	// Canonicalize BOTH sides before isPathInside — EvalSymlinks
	// resolves /var → /private/var on macOS and the workspace-side
	// resolution below goes through the same canonicalization.
	canonVariant, err := filepath.EvalSymlinks(v.StoragePath)
	if err != nil {
		canonVariant = v.StoragePath
	}
	for _, ws := range workspaces {
		if _, err := os.Stat(ws); err != nil {
			if os.IsNotExist(err) {
				continue // ghost — not a pin
			}
			return true, err // unreachable — conservative
		}
		wsResource := filepath.Join(ws, v.Path)
		target, err := os.Readlink(wsResource)
		if err != nil {
			continue // not a symlink — not a pin from this workspace
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(wsResource), target)
		}
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			resolved = filepath.Clean(target)
		}
		if isPathInside(canonVariant, resolved) {
			return true, nil
		}
	}
	return false, nil
}

// sweepBookkeeping removes each path best-effort. Missing paths are
// silently tolerated (RemoveAll semantics); other failures are logged
// to options.Stdout so the operator sees them but the sweep keeps
// going.
func sweepBookkeeping(paths []string, options Options) {
	for _, p := range paths {
		if err := executor.RemoveAllProgress(p, options.Progress); err != nil {
			fmt.Fprintf(options.Stdout, "failed to remove %s: %v\n", p, err)
		}
	}
}
