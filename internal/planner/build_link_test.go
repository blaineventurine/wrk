package planner

import (
	"path/filepath"
	"testing"

	"wrk/internal/config"
	"wrk/internal/location"
	"wrk/internal/resolver"
	"wrk/internal/workspace"
)

// findAction returns the first action of type T in the plan.
func findAction[T Action](t *testing.T, plan ResourcePlan) T {
	t.Helper()

	for _, planned := range plan.Actions {
		if action, ok := planned.Action.(T); ok {
			return action
		}
	}

	var zero T
	t.Fatalf("no action of type %T found in plan: %+v", zero, plan.Actions)
	return zero
}

func TestBuildLinkInitializeHookHasContext(t *testing.T) {
	root := filepath.FromSlash("/repo")
	workspacePath := filepath.Join(root, "vendor", "bundle")
	sharedPath := filepath.FromSlash("/storage/repo/vendor/bundle/abc123")

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "bundler",
			Path: "vendor/bundle",
			Hooks: map[string][]config.Command{
				"initialize": {
					{
						Run: "bundle install",
						Cwd: "{root}",
						Env: map[string]string{
							"BUNDLE_PATH": "{shared}",
						},
					},
				},
			},
		},
		Root:          root,
		WorkspacePath: workspacePath,
		RelativePath:  "vendor/bundle",
	}

	loc := location.SharedLocation{Path: sharedPath}

	// Neither the shared resource nor the workspace resource exists yet,
	// so the initialize hook must run.
	state := workspace.State{
		SharedExists:    false,
		WorkspaceExists: false,
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}

	init := findAction[InitializeResource](t, plan)

	if init.Context.Root != root {
		t.Errorf("Context.Root = %q, want %q", init.Context.Root, root)
	}

	wantParent := filepath.Join(root, "vendor")
	if init.Context.Parent != wantParent {
		t.Errorf("Context.Parent = %q, want %q", init.Context.Parent, wantParent)
	}

	if init.Context.Match != workspacePath {
		t.Errorf("Context.Match = %q, want %q", init.Context.Match, workspacePath)
	}

	// The regression assertion: {shared} must resolve to loc.Path.
	if init.Context.Shared != sharedPath {
		t.Errorf("Context.Shared = %q, want %q", init.Context.Shared, sharedPath)
	}

	// The symlink must follow the initialize hook and target the shared path.
	link := findAction[Symlink](t, plan)
	if link.Target != sharedPath {
		t.Errorf("Symlink.Target = %q, want %q", link.Target, sharedPath)
	}
	if link.Link != workspacePath {
		t.Errorf("Symlink.Link = %q, want %q", link.Link, workspacePath)
	}
}

func TestBuildLinkNoHookNoSharedIsConflict(t *testing.T) {
	root := filepath.FromSlash("/repo")

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "env",
			Path: ".env",
		},
		Root:          root,
		WorkspacePath: filepath.Join(root, ".env"),
		RelativePath:  ".env",
	}

	loc := location.SharedLocation{
		Path: filepath.FromSlash("/storage/repo/.env"),
	}

	// A resource with no shared copy and no initialize hook is a conflict.
	state := workspace.State{}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(plan.Conflicts), plan.Conflicts)
	}
}

func TestBuildLinkAlreadyLinkedCanonicalizedStorage(t *testing.T) {
	root := filepath.FromSlash("/repo")

	// loc.Path is what wrk wrote into the link...
	sharedPath := filepath.FromSlash("/var/storage/repo/vendor/bundle/abc123")

	instance := resolver.ResourceInstance{
		Resource:      config.Resource{Name: "bundler", Path: "vendor/bundle"},
		Root:          root,
		WorkspacePath: filepath.Join(root, "vendor", "bundle"),
		RelativePath:  "vendor/bundle",
	}

	loc := location.SharedLocation{Path: sharedPath}

	state := workspace.State{
		WorkspaceSymlink: true,
		// Link text matches exactly what wrk wrote.
		WorkspaceLinkText: sharedPath,
		// Canonicalized target differs (e.g. /var -> /private/var) and must
		// NOT be used for the comparison.
		WorkspaceTarget: filepath.FromSlash("/private/var/storage/repo/vendor/bundle/abc123"),
		SharedExists:    true,
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Actions) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf(
			"expected no-op plan, got actions=%+v conflicts=%+v",
			plan.Actions, plan.Conflicts,
		)
	}
}

func TestBuildLinkAlreadyLinkedIsNoOp(t *testing.T) {
	root := filepath.FromSlash("/repo")
	sharedPath := filepath.FromSlash("/storage/repo/vendor/bundle/abc123")

	instance := resolver.ResourceInstance{
		Resource:      config.Resource{Name: "bundler", Path: "vendor/bundle"},
		Root:          root,
		WorkspacePath: filepath.Join(root, "vendor", "bundle"),
		RelativePath:  "vendor/bundle",
	}

	loc := location.SharedLocation{Path: sharedPath}

	// Workspace already symlinks to the correct shared target.
	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: sharedPath,
		WorkspaceTarget:   sharedPath,
		SharedExists:      true,
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Actions) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf(
			"expected empty plan, got actions=%+v conflicts=%+v",
			plan.Actions, plan.Conflicts,
		)
	}
}

func TestBuildRelinkDiscardsLocalCopyWhenSharedExists(t *testing.T) {
	inst := instance()
	loc := sharedLocation()

	// The detach scenario: independent local copy AND shared both exist.
	state := workspace.State{
		WorkspaceExists: true,
		SharedExists:    true,
	}

	// BuildLink must refuse (conflict)...
	link := BuildLink(inst, loc, state)
	if len(link.Conflicts) != 1 {
		t.Fatalf("BuildLink: expected 1 conflict, got %+v", link.Conflicts)
	}

	// ...but BuildRelink discards the local copy and re-links.
	relink := BuildRelink(inst, loc, state)
	if len(relink.Conflicts) != 0 {
		t.Fatalf("BuildRelink: unexpected conflicts: %+v", relink.Conflicts)
	}

	_ = findAction[Remove](t, relink)
	sym := findAction[Symlink](t, relink)
	if sym.Target != loc.Path {
		t.Fatalf("Symlink.Target = %q, want %q", sym.Target, loc.Path)
	}
}

func TestBuildRelinkAdoptsLocalWhenSharedMissing(t *testing.T) {
	inst := instance()
	loc := sharedLocation()

	// Real local copy, shared missing: same as link — adopt via Move.
	state := workspace.State{WorkspaceExists: true}

	plan := BuildRelink(inst, loc, state)

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}
	_ = findAction[Move](t, plan)
	_ = findAction[Symlink](t, plan)
}

func TestBuildRelinkAlreadyLinkedIsNoOp(t *testing.T) {
	inst := instance()
	loc := sharedLocation()

	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: loc.Path,
		SharedExists:      true,
	}

	plan := BuildRelink(inst, loc, state)
	if len(plan.Actions) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("expected no-op, got actions=%+v conflicts=%+v", plan.Actions, plan.Conflicts)
	}
}
