package planner

import (
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

// BuildLink computes the actions required to materialize a resource in the
// current workspace. If a resource has both an independent local copy and a
// shared copy, it reports a conflict rather than risk discarding local work.
func BuildLink(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) ResourcePlan {
	return buildLink(instance, loc, state, false)
}

// BuildRelink is like BuildLink, but reconnects a workspace to shared
// storage by discarding any independent local copy. It never conflicts on
// an existing local copy — the caller has explicitly opted into discarding
// it.
func BuildRelink(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) ResourcePlan {
	return buildLink(instance, loc, state, true)
}

func buildLink(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
	discardLocal bool,
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
		linkToShared(&plan, instance, loc, state, discardLocal)
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
	discardLocal bool,
) {
	if state.WorkspaceExists {
		if !discardLocal {
			plan.AddConflict(
				instance,
				"an independent copy exists (detached); run `wrk relink` "+
					"to discard it and reconnect to shared storage",
			)
			return
		}

		// relink: discard the independent local copy before linking.
		plan.AddAction(instance, Remove{Path: instance.WorkspacePath})
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
