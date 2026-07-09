package engine

import (
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// Detach replaces managed symlinks with independent local copies and
// records the intent BEFORE executing the plan. Recording first means
// a partial execution (or a kill mid-swap) is still classified as
// StateDetached by `wrk status`, not StateConflict — which would
// invite `wrk relink` to destroy the user's independent copy.
//
// Union semantics tolerate planned-but-unexecuted paths: the next
// `wrk detach` completes them; only `wrk link`/`wrk relink` clears the
// entry.
func Detach(
	repo *repository.Repository,
	options Options,
) error {
	plan, err := BuildDetachPlan(repo, options)
	if err != nil {
		return err
	}

	// Record BEFORE printPlan can fail: TestDetachRecordsIntentBeforeExecute
	// pins that a stdout write error still leaves the registry
	// populated so `wrk status` classifies the workspace as
	// StateDetached, not StateConflict. ExecuteDetach also records
	// (idempotently, via recordDetached's union semantics), so the
	// two records collapse to a single load-mutate-save cycle in the
	// common path.
	if !options.DryRun {
		if err := recordDetached(repo, detachedPaths(repo, plan)); err != nil {
			return err
		}
	}

	if err := printPlan(options.Stdout, plan); err != nil {
		return err
	}
	return ExecuteDetach(repo, plan, options)
}

// ExecuteDetach records the detach intent then runs a pre-built plan
// without printing it. Recording still happens BEFORE executor.Execute
// so a partial application (or a kill mid-swap) is classified as
// StateDetached by `wrk status`, not StateConflict — see Detach.
//
// Callers that print the plan themselves — e.g. the CLI's Build ->
// Print -> Confirm -> Execute flow — use this instead of Detach to
// avoid a double-print.
func ExecuteDetach(
	repo *repository.Repository,
	plan planner.Plan,
	options Options,
) error {
	// Dry-run leaves the registry untouched.
	if !options.DryRun {
		if err := recordDetached(repo, detachedPaths(repo, plan)); err != nil {
			return err
		}
	}

	return executePlan(plan, options)
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
