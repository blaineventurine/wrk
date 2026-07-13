package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/repository"
)

// ForgetPlan describes the actions `wrk forget` would take. It is the
// read-only output of BuildForgetPlan: no mutation has occurred yet.
// The CLI (Task 3.3) prints it and the executor (Task 3.2) applies it.
//
// Refusal is set when any detach-registry OR isolation-registry entry
// exists for this repo: forgetting a repo whose managed resources have
// been materialized as independent local copies would leave the
// workspace pointing at a dead symlink target, and forgetting isolated
// variants destroys per-workspace content that hooks cannot reproduce.
// `wrk forget --force` is the escape hatch.
type ForgetPlan struct {
	RepositoryID    string              // <repo-id> segment
	StoragePath     string              // absolute path to <storage>/<repo-id>
	TotalSize       int64               // sum of every regular file under StoragePath (0 if missing)
	VariantCount    int                 // number of variant subdirs on disk
	ResourceCount   int                 // number of distinct resource paths on disk
	RegistryEntries map[string][]string // workspace root -> detached paths (snapshot)
	IsolatedEntries []string            // "<workspaceRoot>: <resourcePath>" per isolation entry, sorted
	Refusal         string              // set when any registry or isolation entry exists; --force overrides
}

// BuildForgetPlan produces a read-only snapshot of what `wrk forget`
// would remove. Nothing on disk, in the VCS, or in the detach registry
// is modified — the returned plan is a preview.
//
// Returns (plan, nil) even when storage is empty: an empty plan tells
// the executor there is nothing to sweep on disk, but there may still
// be registry state to clear, so the plan is still meaningful.
//
// Sweep order:
//
//  1. Compose StoragePath. Whether the subtree exists yet is a runtime
//     question; the executor's cleanup logic is target-driven either way.
//  2. If the subtree exists, sum its size via treeSize (best-effort —
//     partial walks yield a lower bound, same policy as `wrk list --size`)
//     and enumerate variants via scanVariants. A scanVariants error
//     (broken config, resolver failure) is deliberately swallowed:
//     `wrk forget` is the tool users reach for precisely when the config
//     is a mess. The counts stay at zero and the executor still sweeps
//     the on-disk subtree from StoragePath. Only os.Stat errors that are
//     NOT IsNotExist surface — those signal a filesystem-level problem
//     (permission denied, I/O error) worth aborting on.
//  3. loadRegistry snapshots the detach registry and loadIsolation the
//     isolation registry. Any entry in either triggers Refusal (reasons
//     joined by "; ") so the CLI can present a single-line summary. The
//     detach map is copied so callers cannot mutate the on-disk view
//     through the returned plan.
func BuildForgetPlan(repo *repository.Repository, options Options) (ForgetPlan, error) {
	plan := ForgetPlan{
		RepositoryID: repo.RepositoryID,
		StoragePath:  filepath.Join(options.StorageRoot, repo.RepositoryID),
	}

	// Storage may not exist yet (Link never ran) or already be gone
	// (a partial prior forget). Absent storage is a legitimate state
	// — we just leave the on-disk counts at zero.
	if _, err := os.Stat(plan.StoragePath); err == nil {
		// treeSize tolerates partial walk failures internally; the
		// returned error is only for hard filesystem-level trouble
		// that even the tolerant walker gives up on. Best-effort:
		// mirror scanVariants' philosophy and swallow it.
		if size, sizeErr := treeSize(plan.StoragePath); sizeErr == nil {
			plan.TotalSize = size
		}

		// A broken config or resolver failure MUST NOT block forget:
		// the entire point of `wrk forget` is to nuke a busted setup.
		// Zeroed counts still let the CLI display a warning; the
		// executor sweeps StoragePath regardless of what the config
		// thinks lives there.
		if variants, scanErr := scanVariants(repo, options); scanErr == nil {
			plan.VariantCount = len(variants)
			// Distinct resource paths (not names): a glob resource can
			// expand to multiple concrete paths, and each is a
			// separate on-disk subtree the user is forgetting.
			resources := make(map[string]struct{}, len(variants))
			for _, v := range variants {
				resources[v.Path] = struct{}{}
			}
			plan.ResourceCount = len(resources)
		}
	} else if !os.IsNotExist(err) {
		return ForgetPlan{}, err
	}

	reg, err := loadRegistry(repo)
	if err != nil {
		return ForgetPlan{}, err
	}

	var reasons []string

	if len(reg) > 0 {
		// Defensive copy: RegistryEntries is documented as a snapshot,
		// and loadRegistry-then-return-directly would leak the live
		// map through the plan boundary. Sorting the roots makes the
		// refusal message deterministic across map-iteration orders.
		plan.RegistryEntries = make(map[string][]string, len(reg))
		roots := make([]string, 0, len(reg))
		for root, paths := range reg {
			cp := append([]string(nil), paths...)
			plan.RegistryEntries[root] = cp
			roots = append(roots, root)
		}
		sort.Strings(roots)
		reasons = append(reasons, fmt.Sprintf(
			"%d workspace(s) have detached files: %s. Run 'wrk relink --yes' in each workspace to reconnect, then re-run 'wrk forget'.",
			len(roots),
			strings.Join(roots, ", "),
		))
	}

	// Isolated variants hold per-workspace content that hooks cannot
	// reproduce — forgetting the repo destroys them permanently. The
	// registry file is repo-scoped, so entries across ALL workspaces
	// count. Refusal keys on registry content, not disk state: even
	// if the variant directory is already gone, the entry says a
	// workspace still expects it.
	iso, err := loadIsolation(repo)
	if err != nil {
		return ForgetPlan{}, err
	}
	for wsRoot, entries := range iso {
		for resourcePath := range entries {
			plan.IsolatedEntries = append(plan.IsolatedEntries,
				fmt.Sprintf("%s: %s", wsRoot, resourcePath))
		}
	}
	if len(plan.IsolatedEntries) > 0 {
		sort.Strings(plan.IsolatedEntries)
		reasons = append(reasons, fmt.Sprintf(
			"%d isolated variant(s) hold per-workspace content that hooks cannot reproduce: %s",
			len(plan.IsolatedEntries),
			strings.Join(plan.IsolatedEntries, ", "),
		))
	}

	if len(reasons) > 0 {
		plan.Refusal = strings.Join(reasons, "; ")
	}

	return plan, nil
}

// ExecuteForget applies plan.StoragePath removal + registry clear.
// Assumes the caller has already applied safety gates from
// BuildForgetPlan / Confirm (a populated Refusal MUST be resolved by
// --force before reaching here). Idempotent: re-running on an
// already-applied plan is a no-op.
//
// Crash safety uses a rename-then-delete pattern with a sibling
// marker directory:
//
//  1. If <repo-id>/ exists, os.Rename it to <repo-id>.wrk-forgetting/.
//     Rename is atomic on POSIX filesystems: on either side of the
//     syscall the storage tree is a single valid directory.
//  2. os.RemoveAll(<repo-id>.wrk-forgetting/). A crash mid-RemoveAll
//     leaves the marker on disk; the next ExecuteForget's recovery
//     branch below finishes the sweep.
//  3. Under withRegistryLock, clear every detach-registry entry and
//     every isolation-registry entry. Writing {} matches
//     clearDetached's convention (loadRegistry round-trips {} back to
//     an empty map); the isolation clear follows the same
//     skip-save-when-empty policy.
//
// The idempotent-recovery branch that runs BEFORE step 1 finishes a
// prior crash's RemoveAll. A prior crash leaves either:
//   - marker present, primary path absent → recovery RemoveAll's the
//     marker, then step 1 sees no primary path and skips to registry.
//   - marker present, primary path present → user re-created the
//     primary somehow (unlikely; only via a manual mkdir race with
//     the previous crash). Recovery still clears the marker so
//     step 1's rename has a clean target.
//
// A crashed `wrk forget` between the rename and the RemoveAll leaves
// a `<repo-id>.wrk-forgetting/` marker at the storage-root. Both the
// idempotent-recovery branch below AND `wrk gc`'s
// cleanBookkeepingDetect will complete the sweep on the next run,
// so recovery is fully automatic from either command.
func ExecuteForget(repo *repository.Repository, plan ForgetPlan, options Options) error {
	if plan.StoragePath != "" {
		marker := plan.StoragePath + ".wrk-forgetting"

		// Idempotent recovery: a prior crash between rename and
		// RemoveAll left the marker on disk. Finish the delete before
		// the fresh rename so step 1 has a clean target.
		if _, err := os.Stat(marker); err == nil {
			if err := executor.RemoveAllProgress(marker, options.Progress); err != nil {
				return fmt.Errorf("clearing forgetting marker: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return err
		}

		// Fresh rename-then-remove. Missing storage is not an error:
		// forget on a repo that was never Linked, or that was already
		// forgotten, still clears the registry.
		if _, err := os.Stat(plan.StoragePath); err == nil {
			if err := os.Rename(plan.StoragePath, marker); err != nil {
				return fmt.Errorf("marking storage for removal: %w", err)
			}
			if err := executor.RemoveAllProgress(marker, options.Progress); err != nil {
				return fmt.Errorf("removing marked storage: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	// Registry clear under the same flock that serializes detach /
	// clearDetached, so a concurrent `wrk detach` on a sibling
	// workspace cannot silently re-populate the registry between our
	// load and save.
	return withRegistryLock(repo, func() error {
		reg, err := loadRegistry(repo)
		if err != nil {
			return err
		}
		if len(reg) > 0 {
			for k := range reg {
				delete(reg, k)
			}
			if err := saveRegistry(repo, reg); err != nil {
				return err
			}
		}

		// Isolation registry: the storage sweep above just destroyed
		// every isolated variant, so the entries are dead. Same
		// skip-save-when-empty convention as the detach clear —
		// recordIsolation shares this flock, so a concurrent isolate
		// cannot interleave with the load-clear-save.
		iso, err := loadIsolation(repo)
		if err != nil {
			return err
		}
		if len(iso) == 0 {
			return nil
		}
		for k := range iso {
			delete(iso, k)
		}
		return saveIsolation(repo, iso)
	})
}
