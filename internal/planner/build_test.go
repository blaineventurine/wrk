package planner

import (
	"testing"

	"wrk/internal/config"
	"wrk/internal/location"
	"wrk/internal/resolver"
	"wrk/internal/workspace"
)

func instance() resolver.ResourceInstance {
	return resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "node",
			Path: "node_modules",
		},
		WorkspacePath: "/repo/node_modules",
	}
}

func sharedLocation() location.SharedLocation {
	return location.SharedLocation{
		Path: "/shared/node_modules/abc123",
	}
}

func TestAlreadyLinked(t *testing.T) {
	loc := sharedLocation()

	plan := BuildLink(
		instance(),
		loc,
		workspace.State{
			WorkspaceSymlink:  true,
			WorkspaceLinkText: loc.Path,
			WorkspaceTarget:   loc.Path,
			SharedExists:      true,
		},
	)

	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected no actions, got %d",
			len(plan.Actions),
		)
	}
}

func TestExistingSharedLinksWorkspace(t *testing.T) {
	plan := BuildLink(
		instance(),
		sharedLocation(),
		workspace.State{
			SharedExists: true,
		},
	)

	if len(plan.Actions) != 1 {
		t.Fatalf(
			"expected 1 action, got %d",
			len(plan.Actions),
		)
	}

	if _, ok := plan.Actions[0].Action.(Symlink); !ok {
		t.Fatalf(
			"expected Symlink, got %T",
			plan.Actions[0].Action,
		)
	}
}

func TestExistingWorkspaceMovesToShared(t *testing.T) {
	plan := BuildLink(
		instance(),
		sharedLocation(),
		workspace.State{
			WorkspaceExists: true,
		},
	)

	if len(plan.Actions) != 3 {
		t.Fatalf(
			"expected 3 actions, got %d",
			len(plan.Actions),
		)
	}

	if _, ok := plan.Actions[0].Action.(CreateDirectory); !ok {
		t.Fatal("expected CreateDirectory")
	}

	if _, ok := plan.Actions[1].Action.(Move); !ok {
		t.Fatal("expected Move")
	}

	if _, ok := plan.Actions[2].Action.(Symlink); !ok {
		t.Fatal("expected Symlink")
	}
}

func TestHasConflicts(t *testing.T) {
	var plan Plan

	if plan.HasConflicts() {
		t.Fatal("expected false")
	}

	plan.Conflicts = append(
		plan.Conflicts,
		Conflict{},
	)

	if !plan.HasConflicts() {
		t.Fatal("expected true")
	}
}

func TestConflictWhenWorkspaceAndSharedExist(t *testing.T) {
	plan := BuildLink(
		instance(),
		sharedLocation(),
		workspace.State{
			WorkspaceExists: true,
			SharedExists:    true,
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

func TestMissingEverythingWithoutInitializeConflicts(t *testing.T) {
	plan := BuildLink(
		instance(),
		sharedLocation(),
		workspace.State{},
	)

	if len(plan.Actions) != 1 {
		t.Fatalf(
			"expected 1 actions, got %d",
			len(plan.Actions),
		)
	}

	if _, ok := plan.Actions[0].Action.(CreateDirectory); !ok {
		t.Fatal("expected CreateDirectory")
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict, got %d",
			len(plan.Conflicts),
		)
	}
}

func TestMissingEverythingWithInitialize(t *testing.T) {
	i := instance()

	i.Resource.Hooks = map[string][]config.Command{
		"initialize": {
			{
				Run: "bundle install",
			},
		},
	}

	plan := BuildLink(
		i,
		sharedLocation(),
		workspace.State{},
	)

	if len(plan.Actions) != 3 {
		t.Fatalf(
			"expected 3 actions, got %d",
			len(plan.Actions),
		)
	}

	if _, ok := plan.Actions[0].Action.(CreateDirectory); !ok {
		t.Fatal("expected CreateDirectory")
	}

	if _, ok := plan.Actions[1].Action.(InitializeResource); !ok {
		t.Fatal("expected Run")
	}

	if _, ok := plan.Actions[2].Action.(Symlink); !ok {
		t.Fatal("expected Symlink")
	}
}

func TestWrongSymlinkRelinks(t *testing.T) {
	plan := BuildLink(
		instance(),
		sharedLocation(),
		workspace.State{
			WorkspaceSymlink: true,
			WorkspaceTarget:  "/shared/node_modules/old",
			SharedExists:     true,
		},
	)

	if len(plan.Actions) != 2 {
		t.Fatalf(
			"expected 2 actions, got %d",
			len(plan.Actions),
		)
	}

	if _, ok := plan.Actions[0].Action.(Remove); !ok {
		t.Fatal("expected Remove")
	}

	if _, ok := plan.Actions[1].Action.(Symlink); !ok {
		t.Fatal("expected Symlink")
	}
}
