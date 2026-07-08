package planner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
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

// TestBuildLinkBrokenSymlinkToMissingSharedRebuilds pins H6: a symlink
// whose LinkText matches loc.Path but whose shared bytes are gone must
// NOT short-circuit as "already linked". buildLink is required to fall
// through so provisionShared (or linkToShared, when SharedExists is
// true elsewhere) reruns and repairs the dangling pointer.
//
// Setup: LinkText matches loc.Path, SharedExists = false, no initialize
// hook, no local copy. The correct behavior is a conflict from the
// "shared missing, no repair path" fallthrough — not the historical
// silent no-op that left the caller with a dangling link.
func TestBuildLinkBrokenSymlinkToMissingSharedRebuilds(t *testing.T) {
	root := filepath.FromSlash("/repo")
	sharedPath := filepath.FromSlash("/storage/repo/vendor/bundle/abc123")

	instance := resolver.ResourceInstance{
		Resource:      config.Resource{Name: "bundler", Path: "vendor/bundle"},
		Root:          root,
		WorkspacePath: filepath.Join(root, "vendor", "bundle"),
		RelativePath:  "vendor/bundle",
	}
	loc := location.SharedLocation{Path: sharedPath}

	// LinkText matches loc.Path exactly — pre-H6 this was the entire
	// "already linked" test. SharedExists is false: the shared bytes
	// have been removed under us.
	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: sharedPath,
		WorkspaceTarget:   sharedPath,
		SharedExists:      false,
	}

	plan := BuildLink(instance, loc, state)

	// No initialize hook + no local copy => "no repair path" conflict.
	// The historical bug was to return an empty plan (no actions, no
	// conflicts), leaving the workspace pointing at nothing.
	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict (dangling link with no repair path), got %d: %+v",
			len(plan.Conflicts),
			plan.Conflicts,
		)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("expected 0 actions, got %+v", plan.Actions)
	}
}

// TestBuildLinkBrokenSymlinkToMissingSharedRunsHook exercises H6's
// happy path: same dangling symlink, but this time an initialize hook
// is configured so provisionShared can rebuild. The plan must include
// the hook AND the trailing Symlink — the latter's own atomic Lstat+
// Remove path (executor) replaces the dangling link only AFTER the
// hook succeeds. See H2 for the reason there is no separate Remove.
func TestBuildLinkBrokenSymlinkToMissingSharedRunsHook(t *testing.T) {
	root := filepath.FromSlash("/repo")
	sharedPath := filepath.FromSlash("/storage/repo/vendor/bundle/abc123")

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "bundler",
			Path: "vendor/bundle",
			Hooks: map[string][]config.Command{
				"initialize": {{Run: "bundle install --path {shared}"}},
			},
		},
		Root:          root,
		WorkspacePath: filepath.Join(root, "vendor", "bundle"),
		RelativePath:  "vendor/bundle",
	}
	loc := location.SharedLocation{Path: sharedPath}

	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: sharedPath,
		WorkspaceTarget:   sharedPath,
		SharedExists:      false,
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}

	// The plan must NOT emit a Remove for the dangling symlink — the
	// trailing Symlink action replaces it atomically once the hook has
	// succeeded. See H2.
	for _, planned := range plan.Actions {
		if _, ok := planned.Action.(Remove); ok {
			t.Fatalf(
				"unexpected Remove action for stale symlink; plan=%+v",
				plan.Actions,
			)
		}
	}

	// The hook must run and a Symlink must follow it.
	_ = findAction[InitializeResource](t, plan)
	sym := findAction[Symlink](t, plan)
	if sym.Target != sharedPath {
		t.Errorf("Symlink.Target = %q, want %q", sym.Target, sharedPath)
	}
}

// TestBuildLinkRefusesToReplaceUserSymlinkTargetingOutsideStorage pins
// H1: a workspace-side symlink that the user manually pointed at
// something OUTSIDE the wrk storage tree must NOT be silently replaced.
// buildLink is required to emit a conflict naming the target so the
// operator can decide whether to `wrk relink` (which discards the
// user's link) or remove the symlink themselves.
func TestBuildLinkRefusesToReplaceUserSymlinkTargetingOutsideStorage(t *testing.T) {
	root := filepath.FromSlash("/repo")
	sharedPath := filepath.FromSlash("/storage/repo/vendor/bundle/abc123")
	// User-created target completely outside the wrk storage tree —
	// no shared ancestor with loc.Path.
	userTarget := filepath.FromSlash("/home/user/my-bundle")

	instance := resolver.ResourceInstance{
		Resource:      config.Resource{Name: "bundler", Path: "vendor/bundle"},
		Root:          root,
		WorkspacePath: filepath.Join(root, "vendor", "bundle"),
		RelativePath:  "vendor/bundle",
	}
	loc := location.SharedLocation{Path: sharedPath}

	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: userTarget,
		WorkspaceTarget:   userTarget,
		SharedExists:      true, // Shared bytes exist — the historical bug
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 1 {
		t.Fatalf(
			"expected 1 conflict for user symlink, got %d: %+v",
			len(plan.Conflicts),
			plan.Conflicts,
		)
	}

	// The conflict message must name the user's target so they can act
	// on it without re-inspecting the filesystem.
	if !strings.Contains(plan.Conflicts[0].Message, userTarget) {
		t.Errorf(
			"conflict message %q does not mention user target %q",
			plan.Conflicts[0].Message, userTarget,
		)
	}

	// Critically: no Remove, no Symlink — the user's link is left alone
	// so the executor never gets a chance to clobber it.
	if len(plan.Actions) != 0 {
		t.Fatalf(
			"expected 0 actions (user link must not be touched), got %+v",
			plan.Actions,
		)
	}
}

// TestBuildLinkReplacesWrkManagedStaleSymlink is H1's companion: a
// stale symlink pointing at the WRONG shared subdirectory (a variant
// left over from a previous fingerprint, but still under the storage
// tree) is still a wrk-written link. buildLink is required to replace
// it silently — the target sits under our own storage, so there is no
// user intent to preserve. The Symlink action alone handles the
// atomic swap (see H2 — no Remove is emitted).
func TestBuildLinkReplacesWrkManagedStaleSymlink(t *testing.T) {
	root := filepath.FromSlash("/repo")
	sharedPath := filepath.FromSlash("/storage/repo/vendor/bundle/abc123")
	// Old wrk-written target: same resource directory, previous
	// fingerprint variant.
	staleWrkTarget := filepath.FromSlash("/storage/repo/vendor/bundle/oldfp")

	instance := resolver.ResourceInstance{
		Resource:      config.Resource{Name: "bundler", Path: "vendor/bundle"},
		Root:          root,
		WorkspacePath: filepath.Join(root, "vendor", "bundle"),
		RelativePath:  "vendor/bundle",
	}
	loc := location.SharedLocation{Path: sharedPath}

	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: staleWrkTarget,
		WorkspaceTarget:   staleWrkTarget,
		SharedExists:      true,
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflict: %+v", plan.Conflicts)
	}

	// H2: no Remove — the trailing Symlink action's own Lstat+Remove
	// handles the atomic replace.
	for _, planned := range plan.Actions {
		if _, ok := planned.Action.(Remove); ok {
			t.Fatalf(
				"unexpected Remove; plan should let the trailing Symlink "+
					"handle atomic replace: %+v",
				plan.Actions,
			)
		}
	}

	sym := findAction[Symlink](t, plan)
	if sym.Target != sharedPath {
		t.Errorf("Symlink.Target = %q, want %q", sym.Target, sharedPath)
	}
	if sym.Link != instance.WorkspacePath {
		t.Errorf("Symlink.Link = %q, want %q", sym.Link, instance.WorkspacePath)
	}
}

// TestBuildLinkStaleSymlinkPlanHasNoRemove pins H2 directly: given a
// stale wrk-managed symlink and a hook-based provisioning path, the
// plan must sequence CreateDirectory → InitializeResource → Symlink,
// with NO explicit Remove for the stale symlink. If the hook fails
// mid-plan, the old symlink is still on disk pointing at the previous
// (intact) shared target — the historical Remove-first ordering
// destroyed that recovery path.
func TestBuildLinkStaleSymlinkPlanHasNoRemove(t *testing.T) {
	root := filepath.FromSlash("/repo")
	sharedPath := filepath.FromSlash("/storage/repo/node_modules/newfp")
	staleWrkTarget := filepath.FromSlash("/storage/repo/node_modules/oldfp")

	instance := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "node",
			Path: "node_modules",
			Hooks: map[string][]config.Command{
				"initialize": {{Run: "npm ci --prefix {shared}"}},
			},
		},
		Root:          root,
		WorkspacePath: filepath.Join(root, "node_modules"),
		RelativePath:  "node_modules",
	}
	loc := location.SharedLocation{Path: sharedPath}

	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: staleWrkTarget,
		WorkspaceTarget:   staleWrkTarget,
		SharedExists:      false, // new variant not yet provisioned
	}

	plan := BuildLink(instance, loc, state)

	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflict: %+v", plan.Conflicts)
	}

	// Order matters — no Remove should appear, and the sequence must
	// end with a Symlink so a mid-plan hook failure leaves the old
	// link intact.
	kinds := make([]string, 0, len(plan.Actions))
	for _, planned := range plan.Actions {
		switch planned.Action.(type) {
		case Remove:
			t.Fatalf(
				"unexpected Remove in plan (H2 requires deferred atomic "+
					"replace via trailing Symlink): %+v",
				plan.Actions,
			)
		case CreateDirectory:
			kinds = append(kinds, "CreateDirectory")
		case InitializeResource:
			kinds = append(kinds, "InitializeResource")
		case Symlink:
			kinds = append(kinds, "Symlink")
		default:
			kinds = append(kinds, "other")
		}
	}
	if len(kinds) < 3 || kinds[len(kinds)-1] != "Symlink" {
		t.Fatalf(
			"expected trailing Symlink after Create+Initialize, got %v",
			kinds,
		)
	}
}
