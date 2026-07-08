package engine

import (
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// Detach makes the current workspace independent of shared resources by
// replacing managed symlinks with independent local copies.
//
// The record of what has been detached is written BEFORE the executor
// runs the plan (intent-record semantics — see recordDetached). If the
// plan partially executes, or the process is killed between the
// executor's final swap and this function's return, the successfully
// materialized files are already covered by a registry entry so
// `wrk status` classifies them as StateDetached rather than
// StateConflict — which would otherwise invite `wrk relink` to destroy
// the user's independent copy.
//
// Union semantics tolerate planned-but-unexecuted paths: the next
// `wrk detach` completes them; only `wrk link`/`wrk relink` clears the
// entry. On plan failure the intent is intentionally NOT rolled back —
// see the audit's C2/C3 findings for why partial-execution recovery
// depends on the registry surviving.
func Detach(
	repo *repository.Repository,
	options Options,
) error {
	plan, err := BuildDetachPlan(repo, options)
	if err != nil {
		return err
	}

	// Dry-run must leave the registry untouched (planning is the only
	// side effect the user asked for). runPlan below still prints the
	// plan and returns without executing.
	if !options.DryRun {
		if err := recordDetached(repo, detachedPaths(repo, plan)); err != nil {
			return err
		}
	}

	return runPlan(plan, options)
}

// detachedPaths returns the workspace-relative paths touched by Detach
// actions in the plan.
func detachedPaths(
	repo *repository.Repository,
	plan planner.Plan,
) []string {
	var paths []string

	for _, planned := range plan.Actions {
		detach, ok := planned.Action.(planner.Detach)
		if !ok {
			continue
		}

		rel, err := filepath.Rel(repo.Root, detach.Link)
		if err != nil {
			continue
		}

		paths = append(paths, filepath.ToSlash(rel))
	}

	return paths
}
