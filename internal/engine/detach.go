package engine

import (
	"path/filepath"

	"wrk/internal/planner"
	"wrk/internal/repository"
)

// Detach makes the current workspace independent of shared resources by
// replacing managed symlinks with independent local copies.
//
// On success (and when not a dry run), it records which resources were
// detached so that `wrk status` can distinguish a deliberate detach from a
// coincidental conflict. The record is cleared by `link`/`relink`.
func Detach(
	repo *repository.Repository,
	options Options,
) error {
	plan, err := BuildDetachPlan(repo, options)
	if err != nil {
		return err
	}

	if err := runPlan(plan, options); err != nil {
		return err
	}

	if options.DryRun {
		return nil
	}

	return recordDetached(repo, detachedPaths(repo, plan))
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
