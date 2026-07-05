package engine

import (
	"fmt"

	"wrk/internal/executor"
	"wrk/internal/repository"
)

// Link initializes or repairs the current workspace.
func Link(
	repo *repository.Repository,
	options Options,
) error {
	plan, err := BuildLinkPlan(
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
