package engine

import (
	"wrk/internal/planner"
	"wrk/internal/repository"
)

// BuildDetachPlan builds the plan to detach the current workspace from
// shared resources.
func BuildDetachPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	return buildPlan(
		repo,
		options,
		nil, // detach never modifies ignore rules
		planner.BuildDetach,
	)
}
