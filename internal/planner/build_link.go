package planner

import (
	"path/filepath"

	"wrk/internal/location"
	"wrk/internal/resolver"
	"wrk/internal/workspace"
)

// BuildLink computes the actions required to materialize a resource in the
// current workspace.
func BuildLink(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) ResourcePlan {
	plan := ResourcePlan{}

	// An existing managed symlink is either already correct (no-op) or
	// stale and must be removed before we reconsider the state.
	if state.WorkspaceSymlink {
		if state.WorkspaceLinkText == loc.Path {
			return plan // Already linked correctly.
		}

		plan.AddAction(instance, Remove{Path: instance.WorkspacePath})
		state.WorkspaceExists = false
	}

	if state.SharedExists {
		linkToShared(&plan, instance, loc, state)
		return plan
	}

	provisionShared(&plan, instance, loc, state)
	return plan
}

// linkToShared handles the case where the shared resource already exists.
func linkToShared(
	plan *ResourcePlan,
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) {
	if state.WorkspaceExists {
		plan.AddConflict(
			instance,
			"workspace resource exists but shared resource already exists",
		)
		return
	}

	symlinkIntoWorkspace(plan, instance, loc)
}

// provisionShared handles the case where the shared resource does not yet
// exist and must be created by adopting a workspace copy, running an
// initialize hook, or reported as a conflict.
func provisionShared(
	plan *ResourcePlan,
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) {
	switch {
	case state.WorkspaceExists:
		// Adopt the existing workspace copy as the shared resource.
		ensureSharedParent(plan, instance, loc)
		plan.AddAction(instance, Move{
			Source:      instance.WorkspacePath,
			Destination: loc.Path,
		})
		symlinkIntoWorkspace(plan, instance, loc)

	case hasInitializeHook(instance):
		// Create the shared resource via its initialize hook.
		ensureSharedParent(plan, instance, loc)
		plan.AddAction(instance, InitializeResource{
			Description: "Initialize " + instance.Resource.Name,
			Context:     instance.Context(loc.Path),
			Commands:    instance.Resource.Hooks["initialize"],
		})
		symlinkIntoWorkspace(plan, instance, loc)

	case !instance.Resource.ShouldCreate():
		// Provisioned out-of-band; skip quietly rather than blocking the
		// rest of the plan.

	default:
		plan.AddConflict(
			instance,
			"shared resource does not exist and no initialize hook is configured",
		)
	}
}

func symlinkIntoWorkspace(
	plan *ResourcePlan,
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
) {
	plan.AddAction(instance, Symlink{
		Link:   instance.WorkspacePath,
		Target: loc.Path,
	})
}

func ensureSharedParent(
	plan *ResourcePlan,
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
) {
	plan.AddAction(instance, CreateDirectory{
		Path: filepath.Dir(loc.Path),
	})
}

func hasInitializeHook(instance resolver.ResourceInstance) bool {
	return len(instance.Resource.Hooks["initialize"]) > 0
}
