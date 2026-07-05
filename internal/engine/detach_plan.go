package engine

import (
	"wrk/internal/config"
	"wrk/internal/location"
	"wrk/internal/planner"
	"wrk/internal/repository"
	"wrk/internal/resolver"
	"wrk/internal/workspace"
)

// BuildDetachPlan builds the plan required to detach the current workspace
// from shared resources.
func BuildDetachPlan(
	repo *repository.Repository,
	options Options,
) (planner.Plan, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return planner.Plan{}, err
	}

	var plan planner.Plan

	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(
			repo.Root,
			resource,
		)
		if err != nil {
			return planner.Plan{}, err
		}

		for _, instance := range instances {
			loc, err := location.For(
				options.StorageRoot,
				repo.RepositoryID,
				instance,
			)
			if err != nil {
				return planner.Plan{}, err
			}

			state, err := workspace.Inspect(
				instance.WorkspacePath,
				loc.Path,
			)
			if err != nil {
				return planner.Plan{}, err
			}

			resourcePlan := planner.BuildDetach(
				instance,
				loc,
				state,
			)

			plan.AddResourcePlan(resourcePlan)
		}
	}

	return plan, nil
}
