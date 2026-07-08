package planner

import (
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
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

	// A conflict must not queue any actions: nothing should be created
	// when there is nothing to provision from.
	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected 0 actions, got %d: %+v",
			len(plan.Actions),
			plan.Actions,
		)
	}

	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict, got %d",
			len(plan.Conflicts),
		)
	}
}

func TestMissingEverythingWithCreateFalseSkips(t *testing.T) {
	create := false

	inst := instance()
	inst.Resource.Create = &create // create: false → provisioned out-of-band

	plan := BuildLink(
		inst,
		sharedLocation(),
		workspace.State{},
	)

	// Nothing to adopt, no initialize hook, and creation is disabled:
	// the instance is skipped without blocking the rest of the plan.
	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected 0 actions, got %d: %+v",
			len(plan.Actions),
			plan.Actions,
		)
	}

	if len(plan.Conflicts) != 0 {
		t.Fatalf(
			"expected 0 conflicts, got %d: %+v",
			len(plan.Conflicts),
			plan.Conflicts,
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

// TestWrongSymlinkRelinks pins that a stale, wrk-managed workspace
// symlink (target under the storage tree, wrong variant) is repaired
// atomically by a single Symlink action. Historically buildLink
// emitted a separate Remove-then-Symlink pair, but that ordering left
// the workspace with no symlink at all if the intermediate steps
// (CreateDirectory, InitializeResource) failed. The trailing Symlink
// action's own Lstat+Remove path (executor) handles the atomic replace
// once every prerequisite has succeeded — see H2 in the audit.
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

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}

	if len(plan.Actions) != 1 {
		t.Fatalf(
			"expected 1 action (Symlink), got %d: %+v",
			len(plan.Actions),
			plan.Actions,
		)
	}

	if _, ok := plan.Actions[0].Action.(Symlink); !ok {
		t.Fatalf(
			"expected Symlink, got %T",
			plan.Actions[0].Action,
		)
	}
}

func TestBuildLinkMissingNoHookCreateTrueIsConflict(t *testing.T) {
	root := filepath.FromSlash("/repo")

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "env",
			Path: ".env",
			// Create defaults to true.
		},
		Root:          root,
		WorkspacePath: filepath.Join(root, ".env"),
		RelativePath:  ".env",
	}

	loc := location.SharedLocation{Path: filepath.FromSlash("/storage/repo/.env")}

	plan := BuildLink(instance, loc, workspace.State{})

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(plan.Conflicts), plan.Conflicts)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("expected no actions on conflict, got %+v", plan.Actions)
	}
}

func TestBuildLinkMissingNoHookCreateFalseSkips(t *testing.T) {
	root := filepath.FromSlash("/repo")
	create := false

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name:   "env",
			Path:   ".env",
			Create: &create, // create: false
		},
		Root:          root,
		WorkspacePath: filepath.Join(root, ".env"),
		RelativePath:  ".env",
	}

	loc := location.SharedLocation{Path: filepath.FromSlash("/storage/repo/.env")}

	plan := BuildLink(instance, loc, workspace.State{})

	if len(plan.Conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %+v", plan.Conflicts)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("expected no actions (skip), got %+v", plan.Actions)
	}
}

func TestBuildLinkCreateFalseStillAdoptsExistingCopy(t *testing.T) {
	root := filepath.FromSlash("/repo")
	create := false
	sharedPath := filepath.FromSlash("/storage/repo/.env")

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name:   "env",
			Path:   ".env",
			Create: &create,
		},
		Root:          root,
		WorkspacePath: filepath.Join(root, ".env"),
		RelativePath:  ".env",
	}

	loc := location.SharedLocation{Path: sharedPath}

	// A real workspace copy exists; create:false must NOT prevent adopting it.
	state := workspace.State{WorkspaceExists: true}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}

	// Expect CreateDirectory + Move + Symlink.
	_ = findAction[Move](t, plan)
	_ = findAction[Symlink](t, plan)
}
