package engine

import (
	"fmt"

	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// Link initializes or repairs the current workspace.
func Link(repo *repository.Repository, options Options) error {
	plan, err := BuildLinkPlan(repo, options)
	if err != nil {
		return err
	}

	if err := runPlan(plan, options); err != nil {
		return err
	}

	if options.DryRun {
		return nil
	}

	// A successful link reconnects the workspace to shared storage, so
	// clear any prior detach record.
	return clearDetached(repo)
}

// Relink reconnects the current workspace to shared storage, discarding any
// independent local copies created by a previous `detach`.
func Relink(repo *repository.Repository, options Options) error {
	plan, err := BuildRelinkPlan(repo, options)
	if err != nil {
		return err
	}

	if err := runPlan(plan, options); err != nil {
		return err
	}

	if options.DryRun {
		return nil
	}

	return clearDetached(repo)
}

// runPlan prints, validates, and (unless dry-run) executes a plan.
func runPlan(plan planner.Plan, options Options) error {
	if err := printPlan(options.Stdout, plan); err != nil {
		return err
	}

	if plan.HasConflicts() {
		return fmt.Errorf(
			"%d conflict(s) — see plan output above",
			len(plan.Conflicts),
		)
	}

	if options.DryRun {
		return nil
	}

	return executor.Execute(plan)
}
