package engine

import (
	"wrk/internal/planner"
	"wrk/internal/repository"
)

// BuildLinkPlan builds an execution plan for the current workspace.
func BuildLinkPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	return buildPlan(repo, options, ignorePreparer(repo), planner.BuildLink)
}
