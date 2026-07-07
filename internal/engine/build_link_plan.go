package engine

import (
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// BuildLinkPlan builds an execution plan for the current workspace.
func BuildLinkPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	return buildPlan(repo, options, ignorePreparer(repo), planner.BuildLink)
}
