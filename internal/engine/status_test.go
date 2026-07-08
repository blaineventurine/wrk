package engine

import (
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

func TestDeriveState(t *testing.T) {
	const shared = "/storage/repo/node/abc123"

	loc := location.SharedLocation{Path: shared}

	// A resource that has an initialize hook.
	withHook := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "node",
			Path: "node_modules",
			Hooks: map[string][]config.Command{
				"initialize": {{Run: "yarn install"}},
			},
		},
	}

	// A plain resource with no hook (ShouldCreate defaults to true).
	plain := resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "env",
			Path: ".env",
		},
	}

	cases := []struct {
		name  string
		inst  resolver.ResourceInstance
		state workspace.State
		want  State
	}{
		{
			name: "linked",
			inst: plain,
			state: workspace.State{
				WorkspaceSymlink:  true,
				WorkspaceLinkText: shared,
				SharedExists:      true,
			},
			want: StateLinked,
		},
		{
			name: "stale symlink points elsewhere",
			inst: plain,
			state: workspace.State{
				WorkspaceSymlink:  true,
				WorkspaceLinkText: "/somewhere/else",
			},
			want: StateStale,
		},
		{
			name: "conflict: local copy and shared both exist",
			inst: plain,
			state: workspace.State{
				WorkspaceExists: true,
				SharedExists:    true,
			},
			want: StateConflict,
		},
		{
			name: "missing: shared exists, nothing local",
			inst: plain,
			state: workspace.State{
				SharedExists: true,
			},
			want: StateMissing,
		},
		{
			name: "not linked: local copy, no shared",
			inst: plain,
			state: workspace.State{
				WorkspaceExists: true,
			},
			want: StateNotLinked,
		},
		{
			name:  "pending: no local, no shared, hook available",
			inst:  withHook,
			state: workspace.State{},
			want:  StatePending,
		},
		{
			name:  "absent: no local, no shared, no hook",
			inst:  plain,
			state: workspace.State{},
			want:  StateAbsent,
		},

		// H6: a symlink whose LinkText matches loc.Path but whose
		// shared bytes are gone reads through as ENOENT. Historically
		// this classified as StateLinked (a no-op status), which
		// silently accepted a dangling link — every subsequent access
		// through the workspace symlink would 404. Surface it as stale
		// so `wrk link` re-provisions.
		{
			name: "stale: symlink matches loc.Path but shared is missing",
			inst: plain,
			state: workspace.State{
				WorkspaceSymlink:  true,
				WorkspaceLinkText: shared,
				SharedExists:      false,
			},
			want: StateStale,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveState(tc.inst, loc, tc.state)
			if got != tc.want {
				t.Fatalf("deriveState = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeriveStateStaleTakesPrecedence confirms that a symlink is classified
// by its target before shared/local existence is considered — a stale link
// must not be reported as "conflict" just because a shared copy exists.
func TestDeriveStateStaleTakesPrecedence(t *testing.T) {
	loc := location.SharedLocation{Path: "/storage/repo/node/abc123"}

	inst := resolver.ResourceInstance{
		Resource: config.Resource{Name: "node", Path: "node_modules"},
	}

	state := workspace.State{
		WorkspaceSymlink:  true,
		WorkspaceLinkText: "/old/storage/node/def456",
		SharedExists:      true, // the *correct* shared path exists...
	}

	// ...but the link points at the old one, so it's stale, not linked.
	if got := deriveState(inst, loc, state); got != StateStale {
		t.Fatalf("deriveState = %q, want %q", got, StateStale)
	}
}
