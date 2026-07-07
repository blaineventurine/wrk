package engine

import (
	"wrk/internal/planner"
	"wrk/internal/repository"
)

// BuildRelinkPlan builds a plan that reconnects the workspace to shared
// storage, discarding independent local copies.
func BuildRelinkPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	return buildPlan(repo, options, ignorePreparer(repo), planner.BuildRelink)
}
