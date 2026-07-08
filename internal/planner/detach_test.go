package planner

import (
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

func detachInstance() resolver.ResourceInstance {
	return resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "node",
			Path: "node_modules",
		},
		WorkspacePath: "/repo/node_modules",
	}
}

func detachLocation() location.SharedLocation {
	return location.SharedLocation{
		Path: "/shared/node_modules/fingerprint",
	}
}

func TestBuildDetachAlreadyDetached(t *testing.T) {
	plan := BuildDetach(
		detachInstance(),
		detachLocation(),
		workspace.State{
			WorkspaceExists:    true,
			WorkspaceDirectory: true,
		},
	)

	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected no actions, got %d",
			len(plan.Actions),
		)
	}

	if len(plan.Conflicts) != 0 {
		t.Fatalf(
			"expected no conflicts, got %d",
			len(plan.Conflicts),
		)
	}
}

func TestBuildDetachMissingWorkspace(t *testing.T) {
	plan := BuildDetach(
		detachInstance(),
		detachLocation(),
		workspace.State{},
	)

	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected no actions, got %d",
			len(plan.Actions),
		)
	}

	if len(plan.Conflicts) != 0 {
		t.Fatalf(
			"expected no conflicts, got %d",
			len(plan.Conflicts),
		)
	}
}

func TestBuildDetachBrokenSymlink(t *testing.T) {
	plan := BuildDetach(
		detachInstance(),
		detachLocation(),
		workspace.State{
			WorkspaceSymlink: true,
			WorkspaceTarget:  "/shared/node_modules/fingerprint",
			SharedExists:     false,
		},
	)

	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected no actions, got %d",
			len(plan.Actions),
		)
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict, got %d",
			len(plan.Conflicts),
		)
	}
}

func TestBuildDetachLinkedResource(t *testing.T) {
	plan := BuildDetach(
		detachInstance(),
		detachLocation(),
		workspace.State{
			WorkspaceSymlink: true,
			WorkspaceTarget:  "/shared/node_modules/fingerprint",
			SharedExists:     true,
		},
	)

	if len(plan.Actions) != 1 {
		t.Fatalf(
			"expected 1 action, got %d",
			len(plan.Actions),
		)
	}

	if _, ok := plan.Actions[0].Action.(Detach); !ok {
		t.Fatalf(
			"expected Detach, got %T",
			plan.Actions[0].Action,
		)
	}

	if len(plan.Conflicts) != 0 {
		t.Fatalf(
			"expected no conflicts, got %d",
			len(plan.Conflicts),
		)
	}
}

// TestBuildDetachRefusesUserCreatedSymlink pins H4: a workspace symlink
// pointing at an external target the user picked themselves must NOT
// be converted into an independent local copy of the SHARED bytes.
// Detach's contract is "materialize the shared side into a real file
// so the workspace no longer depends on wrk" — that only makes sense
// when the symlink already points at wrk's shared side. A user-created
// link expresses intent to track a different target, and copying
// shared bytes over it silently erases that intent.
//
// buildDetach must surface a conflict and produce no actions so the
// executor never touches the user's link.
func TestBuildDetachRefusesUserCreatedSymlink(t *testing.T) {
	// User's target is completely outside the wrk storage tree — no
	// shared ancestor with the detach location.
	userTarget := "/home/user/mytarget"

	plan := BuildDetach(
		detachInstance(),
		detachLocation(),
		workspace.State{
			WorkspaceSymlink:  true,
			WorkspaceLinkText: userTarget,
			WorkspaceTarget:   userTarget,
			SharedExists:      true, // shared bytes exist, but irrelevant
		},
	)

	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected 0 actions (user link must not be touched), got %+v",
			plan.Actions,
		)
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict, got %d: %+v",
			len(plan.Conflicts),
			plan.Conflicts,
		)
	}

	// The conflict message must name the user's target so they can
	// resolve it without shelling out to Readlink.
	msg := plan.Conflicts[0].Message
	if !strings.Contains(msg, userTarget) {
		t.Errorf(
			"conflict message %q does not mention user target %q",
			msg, userTarget,
		)
	}
}

// TestBuildDetachAllowsWrkManagedStaleVariant covers the boundary case
// on the H4 fix: a stale wrk-managed link (target under the storage
// tree but not the current variant) still counts as user-invisible
// wrk state. Detach on it is still a conflict — the link is not the
// live one — but the message must not falsely accuse the user of
// pointing outside storage. This defends the "current variant only"
// interpretation of symlinkTargetIsCurrent.
func TestBuildDetachAllowsWrkManagedStaleVariant(t *testing.T) {
	// Same resource dir, previous fingerprint.
	stale := "/shared/node_modules/oldfp"

	plan := BuildDetach(
		detachInstance(),
		detachLocation(),
		workspace.State{
			WorkspaceSymlink:  true,
			WorkspaceLinkText: stale,
			WorkspaceTarget:   stale,
			SharedExists:      true,
		},
	)

	// A stale wrk link is NOT the current shared location -> conflict.
	// (The user should `wrk relink` first, then `wrk detach`.)
	if len(plan.Actions) != 0 {
		t.Fatalf("expected 0 actions, got %+v", plan.Actions)
	}
	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict, got %d: %+v",
			len(plan.Conflicts),
			plan.Conflicts,
		)
	}
}
