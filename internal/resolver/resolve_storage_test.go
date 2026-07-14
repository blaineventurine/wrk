package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestResolveWithStorage pins the storage-aware resolution contract:
//
//   - non-glob paths yield exactly one workspace-anchored instance and
//     never consult storage;
//   - glob paths union workspace and storage matches, with storage
//     matches re-anchored under the workspace root;
//   - the union is deduplicated and sorted;
//   - storage bookkeeping entries (*.wrk-lock and friends) are never
//     resource matches;
//   - a storage match escaping the storage subtree is skipped instead
//     of producing an instance outside the workspace;
//   - glob expansion re-passes the infrastructure policy: matches whose
//     repository-relative path hits config.DisallowedResourcePath (.git,
//     .jj, the wrk config files, executor-reserved suffixes) are skipped
//     on BOTH sides, while non-glob literals bypass the filter entirely;
//   - a malformed pattern surfaces filepath.ErrBadPattern instead of
//     panicking or silently matching nothing.
func TestResolveWithStorage(t *testing.T) {
	cases := []struct {
		name string
		path string
		// workspace / storage list relpaths created under the
		// workspace root / storage resource root before resolving.
		workspace []string
		storage   []string
		// storageKin lists relpaths created in the PARENT of the
		// storage resource root — bait for `..`-escaping patterns.
		storageKin []string
		// wantRel is the exact, ordered slice of RelativePath values.
		wantRel []string
		// wantErrIs, when non-nil, asserts the error chain instead.
		wantErrIs error
	}{
		{
			name:    "non-glob ignores storage",
			path:    "node_modules",
			storage: []string{"node_modules", "unrelated"},
			wantRel: []string{"node_modules"},
		},
		{
			name:    "non-glob needs no existing path",
			path:    ".env",
			wantRel: []string{".env"},
		},
		{
			// The fresh-`wrk new` scenario: the workspace has none of
			// the glob's directories yet, but a peer workspace already
			// provisioned them in storage.
			name: "glob matches storage for fresh workspace",
			path: "packages/*/node_modules",
			storage: []string{
				"packages/app/node_modules",
				"packages/lib/node_modules",
			},
			wantRel: []string{
				"packages/app/node_modules",
				"packages/lib/node_modules",
			},
		},
		{
			// The storage-side match sorts BEFORE the workspace-side
			// match, so this row fails if the union is merely
			// concatenated instead of sorted.
			name:      "glob unions both sides sorted",
			path:      "apps/*/node_modules",
			workspace: []string{"apps/zeta/node_modules"},
			storage:   []string{"apps/alpha/node_modules"},
			wantRel: []string{
				"apps/alpha/node_modules",
				"apps/zeta/node_modules",
			},
		},
		{
			name:      "overlapping match dedups",
			path:      "apps/*/node_modules",
			workspace: []string{"apps/web/node_modules"},
			storage:   []string{"apps/web/node_modules"},
			wantRel:   []string{"apps/web/node_modules"},
		},
		{
			name: "storage bookkeeping suffixes filtered",
			path: "*",
			storage: []string{
				"keep",
				"cache.wrk-lock",
				"cache.wrk-tmp",
				"cache.wrk-backup",
				"cache.wrk-deleting",
				"cache.wrk-forgetting",
				"cache.wrk-provisioning",
			},
			wantRel: []string{"keep"},
		},
		{
			// The filter is a basename SUFFIX check; a name merely
			// containing a bookkeeping marker mid-string is a
			// legitimate resource.
			name:    "bookkeeping filter is suffix not substring",
			path:    "*",
			storage: []string{"data.wrk-tmp.d"},
			wantRel: []string{"data.wrk-tmp.d"},
		},
		{
			// H7: `*` matches dotfiles under filepath.Match semantics,
			// so glob expansion must re-pass the infrastructure policy
			// or `path: "*"` would sweep .git/.jj/.wrk.yml (and
			// executor scratch names) into management.
			name: "glob filters workspace infrastructure and reserved suffixes",
			path: "*",
			workspace: []string{
				".git/HEAD",
				".jj/repo/store/x",
				".wrk.yml",
				".wrk.local.yml",
				"legit-a/keep",
				"legit-b.wrk-tmp/x",
				"normal.txt",
			},
			wantRel: []string{"legit-a", "normal.txt"},
		},
		{
			// The same policy applies to storage-side matches: a .git
			// that leaked into storage must not be re-anchored into
			// the workspace, while legitimate peer-provisioned entries
			// still union in.
			name:      "glob filters storage-side infrastructure",
			path:      "*",
			workspace: []string{"legit-a/keep", "normal.txt"},
			storage:   []string{".git/config", "legit-c/keep"},
			wantRel:   []string{"legit-a", "legit-c", "normal.txt"},
		},
		{
			// Infrastructure is detected by FIRST path segment, so a
			// nested pattern must drop .git/hooks yet keep siblings.
			name: "nested glob under infrastructure filtered",
			path: "*/hooks",
			workspace: []string{
				".git/hooks/pre-commit",
				"legit-a/hooks/pre-commit",
			},
			wantRel: []string{"legit-a/hooks"},
		},
		{
			// Non-glob literals are NOT filtered: they are rejected at
			// config load, and the resolver silently skipping one here
			// would hide a config bug instead of surfacing it. The
			// literal must reach newInstance and resolve as before.
			name:      "non-glob literal bypasses infrastructure filter",
			path:      ".git",
			workspace: []string{".git/HEAD"},
			wantRel:   []string{".git"},
		},
		{
			// The pattern escapes the storage subtree; the match MUST
			// be skipped (not re-anchored, not an error).
			name:       "storage match escaping storage root skipped",
			path:       "../escape-*",
			storageKin: []string{"escape-me"},
			wantRel:    []string{},
		},
		{
			name:    "glob with no matches on either side",
			path:    "apps/*/node_modules",
			wantRel: []string{},
		},
		{
			name:      "bad pattern returns ErrBadPattern",
			path:      "[",
			wantErrIs: filepath.ErrBadPattern,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "ws")
			storageRoot := filepath.Join(base, "storage", "repo-id")

			for _, dir := range []string{root, storageRoot} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			for _, rel := range tc.workspace {
				touch(t, filepath.Join(root, filepath.FromSlash(rel)))
			}
			for _, rel := range tc.storage {
				touch(t, filepath.Join(storageRoot, filepath.FromSlash(rel)))
			}
			for _, rel := range tc.storageKin {
				touch(t, filepath.Join(
					filepath.Dir(storageRoot), filepath.FromSlash(rel),
				))
			}

			instances, err := ResolveWithStorage(root, storageRoot, config.Resource{
				Name: "res",
				Path: tc.path,
			})

			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("expected error matching %v, got instances %v",
						tc.wantErrIs, instances)
				}
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("error = %v, want chain containing %v", err, tc.wantErrIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveWithStorage: %v", err)
			}

			got := make([]string, 0, len(instances))
			for _, inst := range instances {
				// Every instance is workspace-anchored regardless of
				// which side matched.
				if inst.Root != root {
					t.Errorf("instance %q: Root = %q, want %q",
						inst.RelativePath, inst.Root, root)
				}
				want := filepath.Join(root, filepath.FromSlash(inst.RelativePath))
				if inst.WorkspacePath != want {
					t.Errorf("instance %q: WorkspacePath = %q, want %q",
						inst.RelativePath, inst.WorkspacePath, want)
				}
				got = append(got, inst.RelativePath)
			}

			if !reflect.DeepEqual(got, tc.wantRel) {
				t.Fatalf("RelativePaths = %v, want %v", got, tc.wantRel)
			}
		})
	}
}
