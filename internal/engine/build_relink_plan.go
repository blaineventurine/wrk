package engine

import (
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// BuildRelinkPlan builds a plan that reconnects the workspace to shared
// storage, discarding independent local copies.
func BuildRelinkPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	plan, err := buildPlan(repo, options, ignorePreparer(repo), planner.BuildRelink)
	if err != nil {
		return plan, err
	}
	plan.WorkspaceRoot = repo.Root
	return plan, nil
}
