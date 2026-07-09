package engine

import (
	"fmt"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// Run re-executes a resource's initialize hook against the currently
// linked shared variant, atomically replacing the variant's contents.
// Use this to retry after fixing a hook or to refresh a variant without
// recomputing fingerprints.
//
// The workspace symlink is not touched — a successful Run leaves the
// same variant path in place with new bytes behind it.
//
// Errors surface when:
//   - repo is nil
//   - the resource is not configured
//   - the resource has no initialize hook
//   - the resource is detached in this workspace (swapping the shared
//     variant would have no visible effect on the workspace's
//     independent copy)
//   - the resolver, plan build, or hook execution fails
func Run(
	repo *repository.Repository,
	resourceName string,
	options Options,
) error {
	if repo == nil {
		return fmt.Errorf("Run: nil repo")
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return err
	}

	var target *config.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resourceName {
			target = &cfg.Resources[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("resource %q not configured", resourceName)
	}

	hookCommands, ok := target.Hooks["initialize"]
	if !ok || len(hookCommands) == 0 {
		return fmt.Errorf(
			"resource %q has no initialize hook to run",
			resourceName,
		)
	}

	// Refuse if this workspace has detached the resource: swapping the
	// shared variant would have no effect on the workspace's local copy,
	// so the user's mental model ("wrk run refreshed my resource") would
	// silently be wrong.
	reg, err := loadRegistry(repo)
	if err != nil {
		return err
	}

	instances, err := resolver.Resolve(repo.Root, *target)
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return fmt.Errorf(
			"resource %q resolved to no instances",
			resourceName,
		)
	}

	for _, instance := range instances {
		if isDetached(reg, repo.Root, instance.RelativePath) {
			return fmt.Errorf(
				"resource %q is detached in this workspace; run `wrk relink` first",
				resourceName,
			)
		}
	}

	// Build a plan with one Force=true InitializeResource action per
	// resolved instance. WorkspaceRoot lets ensureContained gate on the
	// same guard the Link path uses.
	plan := planner.Plan{WorkspaceRoot: repo.Root}
	for _, instance := range instances {
		loc, err := location.For(
			options.StorageRoot,
			repo.RepositoryID,
			instance,
		)
		if err != nil {
			return err
		}

		plan.Actions = append(plan.Actions, planner.PlannedAction{
			Instance: instance,
			Action: planner.InitializeResource{
				Description: fmt.Sprintf(
					"re-run initialize hook for %s",
					target.Name,
				),
				Context: instance.Context(loc.Path),
				Commands: hookCommands,
				Force:    true,
			},
		})
	}

	return runPlan(plan, options)
}
