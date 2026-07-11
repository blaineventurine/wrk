package engine

import (
	"fmt"
	"os"

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
//  1. PruneGhosts before anything else — the registry sweep needs the
//     post-prune live-workspace set, and a variant sweep that used a
//     ghost's cached pin set would preserve dead data.
//  2. Sweep orphan detach-registry entries using a FRESH Workspaces()
//     call. Recomputing (rather than trusting plan.OrphanRegistry) is
//     defensive: re-running ExecuteGC after a partial application
//     produces the same result as running it once against a clean
//     plan.
//  3. Delete variants. Each acquires <variant>.wrk-lock non-blocking;
//     a held lock is skipped with a warning (a concurrent wrk link is
//     provisioning). The delete uses rename-then-remove so a crash
//     mid-RemoveAll leaves a .wrk-deleting marker the next gc sweeps.
//  4. Sweep bookkeeping cruft (OrphanedLocks, StaleProvisioning,
//     StaleDeleting, StaleForgetting). Failures here log and continue
//     — leftover cruft is annoying but never corrupts state.
func ExecuteGC(repo *repository.Repository, plan GCPlan, options Options) error {
	var firstErr error
	recordErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	// Step 1: Prune ghosts from VCS metadata. If this fails we bail
	// before any other mutation so the operator can retry from a
	// consistent state — a half-pruned registry against a live-ghost
	// git tree would be worse than doing nothing.
	if _, err := repo.PruneGhosts(); err != nil {
		return err
	}

	// Step 2: Sweep orphan registry entries against a freshly-queried
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

	// Step 3: Delete variants. The per-iteration closure exists so
	// defer runs at loop-body exit, not function exit — otherwise a
	// long DeleteVariants slice would hold every lock until we
	// return.
	for _, v := range plan.DeleteVariants {
		deleteVariant(v, options, recordErr)
	}

	// Step 4: Sweep bookkeeping cruft. Failures are surfaced but do
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
func deleteVariant(v variant, options Options, recordErr func(error)) {
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
