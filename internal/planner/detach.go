package planner

import (
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

func BuildDetach(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) ResourcePlan {
	plan := ResourcePlan{}

	switch {

	case state.WorkspaceSymlink:
		// Only detach a wrk-managed symlink (one pointing at the
		// current shared location for this resource). A user-created
		// symlink to some external target represents intent to track
		// that target — copying shared bytes into place and removing
		// their symlink would silently erase it. Surface the mismatch
		// as a conflict; the user can either `wrk relink` to reset the
		// symlink to shared storage first, or delete their symlink
		// themselves and re-run.
		if !symlinkTargetIsCurrent(loc, state) {
			plan.AddConflict(
				instance,
				"workspace path is a symlink to "+
					displaySymlinkTarget(state)+
					", not to shared storage; wrk cannot detach it "+
					"(the target is not managed by wrk)",
			)
			return plan
		}

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
