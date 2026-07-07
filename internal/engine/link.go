package engine

import (
	"fmt"

	"wrk/internal/executor"
	"wrk/internal/planner"
	"wrk/internal/repository"
)

// Link initializes or repairs the current workspace.
func Link(repo *repository.Repository, options Options) error {
	plan, err := BuildLinkPlan(repo, options)
	if err != nil {
		return err
	}
	return runPlan(plan, options)
}

// Relink reconnects the current workspace to shared storage, discarding any
// independent local copies created by a previous `detach`.
func Relink(repo *repository.Repository, options Options) error {
	plan, err := BuildRelinkPlan(repo, options)
	if err != nil {
		return err
	}
	return runPlan(plan, options)
}

// runPlan prints, validates, and (unless dry-run) executes a plan.
func runPlan(plan planner.Plan, options Options) error {
	if err := printPlan(options.Stdout, plan); err != nil {
		return err
	}

	if plan.HasConflicts() {
		return fmt.Errorf(
			"cannot execute plan due to %d conflict(s)",
			len(plan.Conflicts),
		)
	}

	if options.DryRun {
		return nil
	}

	return executor.Execute(plan)
}
