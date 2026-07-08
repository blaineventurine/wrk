package planner

import (
	"path/filepath"
	"strings"

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

	// Classify an existing workspace symlink before deciding what to do:
	//
	//   H6: even a link whose text matches loc.Path is broken when the
	//       shared bytes are gone (GC'd, race, external cleanup). Fall
	//       through to (re-)provisioning instead of a no-op.
	//
	//   H1: a link whose text does NOT match loc.Path is either a stale
	//       wrk-managed link (target sits under this repo's storage
	//       tree) — silently replace — or a user-created link pointing
	//       somewhere else — refuse and surface as a conflict so the
	//       user's intent isn't erased.
	//
	//   H2: never emit a Remove for the old symlink here. The trailing
	//       Symlink action's own Lstat+Remove path (execute.go) handles
	//       the atomic replace AFTER CreateDirectory + adopt/hook have
	//       succeeded. If a middle step fails, the old symlink is still
	//       on disk pointing at the previous (intact) shared target.
	if state.WorkspaceSymlink {
		if state.WorkspaceLinkText == loc.Path && state.SharedExists {
			return plan // Already linked correctly.
		}

		if state.WorkspaceLinkText != loc.Path &&
			!symlinkTargetIsWrkManaged(loc, state) {
			plan.AddConflict(
				instance,
				"workspace path is a symlink to "+
					displaySymlinkTarget(state)+
					", not to shared storage; run `wrk relink` to "+
					"accept the shared target and discard the current link",
			)
			return plan
		}

		// A symlink is present, but Inspect never sets WorkspaceExists
		// for symlinks (only real files/dirs). The downstream branches
		// already act on WorkspaceExists correctly (skip the adopt-copy
		// path, hit the symlinkIntoWorkspace path), so no local override
		// is needed here.
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

// symlinkTargetIsWrkManaged reports whether an existing workspace
// symlink's target sits within the wrk-managed storage subtree for this
// resource. Stale wrk-written links (target under the storage tree,
// wrong variant) can be silently replaced; user-created links pointing
// anywhere else must be surfaced as a conflict so the user's intent is
// not erased.
//
// wrk always writes an absolute path under the storage root, so a
// relative or missing link text is definitively user-managed. The
// shared location's parent (filepath.Dir(loc.Path)) is the closest
// ancestor guaranteed to sit inside wrk storage: for fingerprinted
// resources it is the resource directory (siblings are other
// variants); for un-fingerprinted resources it is one level higher.
func symlinkTargetIsWrkManaged(
	loc location.SharedLocation,
	state workspace.State,
) bool {
	target := state.WorkspaceLinkText
	if target == "" {
		target = state.WorkspaceTarget
	}
	if target == "" || !filepath.IsAbs(target) {
		return false
	}
	prefix := filepath.Dir(loc.Path)
	if prefix == "" || prefix == "." {
		return false
	}
	return target == prefix || strings.HasPrefix(
		target,
		prefix+string(filepath.Separator),
	)
}

// displaySymlinkTarget picks the best human-readable target for a
// conflict message. LinkText is what the user (or wrk) actually wrote;
// falling back to WorkspaceTarget covers the synthetic-state case that
// only Inspect-bypassing callers hit.
func displaySymlinkTarget(state workspace.State) string {
	if state.WorkspaceLinkText != "" {
		return state.WorkspaceLinkText
	}
	return state.WorkspaceTarget
}

// symlinkTargetIsCurrent reports whether an existing workspace symlink
// already points at the currently-expected shared location for this
// resource. LinkText is authoritative (that's the exact bytes wrk
// wrote); Target (EvalSymlinks-resolved) is a fallback for synthetic
// states that never populate LinkText — production Inspect always
// fills it in.
func symlinkTargetIsCurrent(
	loc location.SharedLocation,
	state workspace.State,
) bool {
	if state.WorkspaceLinkText == loc.Path {
		return true
	}
	return state.WorkspaceLinkText == "" && state.WorkspaceTarget == loc.Path
}
