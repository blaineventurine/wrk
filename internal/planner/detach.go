package planner

import (
	"wrk/internal/location"
	"wrk/internal/resolver"
	"wrk/internal/workspace"
)

func BuildDetach(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) ResourcePlan {
	plan := ResourcePlan{}

	switch {

	case state.WorkspaceSymlink:
		if !state.SharedExists {
			plan.AddConflict(
				instance,
				"shared resource no longer exists",
			)

			return plan
		}

		plan.AddAction(
			instance,
			Detach{
				Link:   instance.WorkspacePath,
				Target: loc.Path,
			},
		)

	case state.WorkspaceDirectory:
		// Already detached.

	default:
		// Nothing to do.
	}

	return plan
}
