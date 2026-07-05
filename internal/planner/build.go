package planner

import (
	"path/filepath"

	"wrk/internal/location"
	"wrk/internal/resolver"
	"wrk/internal/workspace"
)

// Plan computes the actions required to materialize a resource.
func BuildLink(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) ResourcePlan {
	plan := ResourcePlan{}

	workspace := instance.WorkspacePath

	if state.WorkspaceSymlink {
		if state.WorkspaceTarget == loc.Path {
			return plan
		}

		plan.AddAction(
			instance,
			Remove{
				Path: workspace,
			},
		)

		state.WorkspaceExists = false
	}

	if state.SharedExists {
		if state.WorkspaceExists {
			plan.AddConflict(
				instance,
				"workspace resource exists but shared resource already exists",
			)

			return plan
		}

		plan.AddAction(
			instance,
			Symlink{
				Link:   workspace,
				Target: loc.Path,
			},
		)

		return plan
	}

	plan.AddAction(
		instance,
		CreateDirectory{
			Path: filepath.Dir(loc.Path),
		},
	)

	if state.WorkspaceExists {
		plan.AddAction(
			instance,
			Move{
				Source:      workspace,
				Destination: loc.Path,
			},
		)

		plan.AddAction(
			instance,
			Symlink{
				Link:   workspace,
				Target: loc.Path,
			},
		)

		return plan
	}

	initialize := instance.Resource.Hooks["initialize"]

	if len(initialize) > 0 {
		plan.AddAction(
			instance,
			InitializeResource{
				Description: "Initialize " + instance.Resource.Name,
				Commands:    initialize,
			},
		)

		plan.AddAction(
			instance,
			Symlink{
				Link:   workspace,
				Target: loc.Path,
			},
		)

		return plan
	}

	plan.AddConflict(
		instance,
		"shared resource does not exist and no initialize hook is configured",
	)

	return plan
}
