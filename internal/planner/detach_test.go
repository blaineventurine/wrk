package planner

import (
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
