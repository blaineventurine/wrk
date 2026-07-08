package engine

import (
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// BuildDetachPlan builds the plan to detach the current workspace from
// shared resources.
func BuildDetachPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	plan, err := buildPlan(
		repo,
		options,
		nil, // detach never modifies ignore rules
		planner.BuildDetach,
	)
	if err != nil {
		return plan, err
	}
	plan.WorkspaceRoot = repo.Root
	return plan, nil
}
