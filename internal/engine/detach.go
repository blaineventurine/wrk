package engine

import (
	"fmt"
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// Detach makes the current workspace independent of shared resources by
// replacing managed symlinks with independent local copies.
//
// On success (and when not a dry run), it accretes a record of which
// resources have been detached so that `wrk status` can distinguish a
// deliberate detach from a coincidental conflict. Subsequent detaches
// union with the existing record; only `link`/`relink` clears it.
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

	if err := recordDetached(repo, detachedPaths(repo, plan)); err != nil {
		return fmt.Errorf("detach succeeded but failed to update detach record: %w", err)
	}
	return nil
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
