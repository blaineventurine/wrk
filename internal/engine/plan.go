package engine

import (
	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

// resourcePlanner builds the plan for a single resolved resource instance.
type resourcePlanner func(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) planner.ResourcePlan

// buildPlan walks every configured resource, resolves it into concrete
// instances, and applies build to each one.
// buildPlan walks every configured resource, resolves it into concrete
// instances, and applies build to each one.
//
// prepare, if non-nil, runs once after the config is loaded and before any
// planning (used by link to update repository ignore rules).
func buildPlan(
	repo *repository.Repository,
	options Options,
	prepare func(cfg *config.Config) error,
	build resourcePlanner,
) (planner.Plan, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return planner.Plan{}, err
	}
	printWarnings(cfg, options.Stdout)

	if prepare != nil {
		if err := prepare(cfg); err != nil {
			return planner.Plan{}, err
		}
	}

	var plan planner.Plan

	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(repo.Root, resource)
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

			plan.AddResourcePlan(build(instance, loc, state))
		}
	}

	return plan, nil
}

// ignorePreparer builds a prepare hook that ensures wrk-managed paths (and
// the local config override file) are ignored by the VCS.
func ignorePreparer(repo *repository.Repository) func(*config.Config) error {
	return func(cfg *config.Config) error {
		paths := []string{config.LocalFilename}
		for _, r := range cfg.Resources {
			paths = append(paths, r.Path)
		}
		return repo.Prepare(paths...)
	}
}
