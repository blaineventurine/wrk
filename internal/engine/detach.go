package engine

import (
	"fmt"

	"wrk/internal/executor"
	"wrk/internal/repository"
)

// Detach makes the current workspace independent of shared resources.
func Detach(
	repo *repository.Repository,
	options Options,
) error {
	plan, err := BuildDetachPlan(
		repo,
		options,
	)
	if err != nil {
		return err
	}

	if err := printPlan(
		options.Stdout,
		plan,
	); err != nil {
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
