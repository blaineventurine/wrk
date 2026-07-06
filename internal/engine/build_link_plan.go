package engine

import (
	"wrk/internal/config"
	"wrk/internal/planner"
	"wrk/internal/repository"
)

// BuildLinkPlan builds an execution plan for the current workspace.
func BuildLinkPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	return buildPlan(
		repo,
		options,
		func(cfg *config.Config) error {
			paths := make([]string, 0, len(cfg.Resources))
			for _, resource := range cfg.Resources {
				paths = append(paths, resource.Path)
			}
			return repo.Prepare(paths...)
		},
		planner.BuildLink,
	)
}
