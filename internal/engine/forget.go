package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blaineventurine/wrk/internal/repository"
)

// ForgetPlan describes the actions `wrk forget` would take. It is the
// read-only output of BuildForgetPlan: no mutation has occurred yet.
// The CLI (Task 3.3) prints it and the executor (Task 3.2) applies it.
//
// Refusal is set when any detach-registry entry exists for this repo:
// forgetting a repo whose managed resources have been materialized as
// independent local copies would leave the workspace pointing at a
// dead symlink target. `wrk forget --force` is the escape hatch.
type ForgetPlan struct {
	RepositoryID    string              // <repo-id> segment
	StoragePath     string              // absolute path to <storage>/<repo-id>
	TotalSize       int64               // sum of every regular file under StoragePath (0 if missing)
	VariantCount    int                 // number of variant subdirs on disk
	ResourceCount   int                 // number of distinct resource paths on disk
	RegistryEntries map[string][]string // workspace root -> detached paths (snapshot)
	Refusal         string              // set when any registry entry exists; --force overrides
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
//  3. loadRegistry snapshots the detach registry. Any entry triggers
//     Refusal with the list of workspace roots so the CLI can present
//     a single-line summary. The map is copied so callers cannot
//     mutate the on-disk view through the returned plan.
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
		plan.Refusal = fmt.Sprintf(
			"%d workspace(s) have detached files: %s. Run 'wrk relink --yes' in each workspace to reconnect, then re-run 'wrk forget'.",
			len(roots),
			strings.Join(roots, ", "),
		)
	}

	return plan, nil
}
