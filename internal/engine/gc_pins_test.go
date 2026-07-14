package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestWorkspacePinsPath unit-tests the single shared definition of "a
// workspace pins a variant" (used by BOTH the plan-time pin walk and
// the execute-time re-check): the workspace's copy of the variant's
// repo-relative path must be a symlink RESOLVING into the variant's
// canonical storage path. Everything else — a real directory (the
// detached-copy shape), a dangling link, a user symlink pointing
// elsewhere, a missing path — is not a pin.
func TestWorkspacePinsPath(t *testing.T) {
	// canonBase is documented as the EvalSymlinks'd variant path;
	// canonicalize up front (macOS /var → /private/var).
	base := canonPath(t, t.TempDir())
	inner := filepath.Join(base, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := canonPath(t, t.TempDir())

	// A nested relpath in slash form exercises the FromSlash join.
	const rel = "web/node_modules"

	cases := []struct {
		name  string
		setup func(t *testing.T, wsResource string)
		want  bool
	}{
		{
			name: "symlink resolving into the variant",
			setup: func(t *testing.T, wsResource string) {
				if err := os.Symlink(inner, wsResource); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
		{
			name: "symlink to the variant base itself",
			setup: func(t *testing.T, wsResource string) {
				if err := os.Symlink(base, wsResource); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
		{
			name: "real directory (detached copy)",
			setup: func(t *testing.T, wsResource string) {
				if err := os.MkdirAll(wsResource, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "dangling symlink",
			setup: func(t *testing.T, wsResource string) {
				if err := os.Symlink(filepath.Join(base, "never-created"), wsResource); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name: "symlink to a path outside the variant",
			setup: func(t *testing.T, wsResource string) {
				if err := os.Symlink(elsewhere, wsResource); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
		{
			name:  "path missing entirely",
			setup: func(t *testing.T, wsResource string) {},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			wsResource := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(wsResource), 0o755); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, wsResource)
			if got := workspacePinsPath(root, rel, base); got != tc.want {
				t.Errorf("workspacePinsPath(%q, %q, %q) = %v, want %v",
					root, rel, base, got, tc.want)
			}
		})
	}
}

// TestGCPlanKeepsVariantRelocatedBySiblingConfig pins the H2
// data-loss scenario end to end: a sibling worktree whose OWN
// .wrk.yml places the resource at a relpath the primary's literal
// config would never resolve (web/node_modules). Because pins are
// discovered from the VARIANT side — probing Join(root, v.Path) in
// every live root — the sibling's symlink at the variant's own path
// pins it, no matter what the invoking workspace's config says. A gc
// run from the primary MUST keep that variant (KeepVariants, never
// DeleteVariants) and ExecuteGC MUST leave it on disk.
func TestGCPlanKeepsVariantRelocatedBySiblingConfig(t *testing.T) {
	// The primary's tracked config is a glob so its variant scan can
	// SEE the storage subtree the sibling provisioned; the sibling's
	// own working-copy config relocates the resource to the concrete
	// web/node_modules path.
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: \"*/node_modules\"\n",
	})
	featurePath, feature := addGitWorktree(t, primary, "gcpin-feature")
	storage := storageOutside(t)
	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	writeConfig(t, featurePath, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: web/node_modules\n")
	writeFile(t, filepath.Join(featurePath, "web", "node_modules", "dep.js"), "payload\n")

	if err := Link(feature, opts); err != nil {
		t.Fatalf("Link on relocated sibling: %v", err)
	}

	variantPath := filepath.Join(storage, primary.RepositoryID, "web", "node_modules")
	if _, err := os.Stat(filepath.Join(variantPath, "dep.js")); err != nil {
		t.Fatalf("fixture: sibling's variant not adopted into storage: %v", err)
	}

	plan, err := BuildGCPlan(primary, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan from primary: %v", err)
	}
	if len(plan.UnreachableWorkspaces) != 0 {
		t.Errorf("UnreachableWorkspaces = %v, want empty", plan.UnreachableWorkspaces)
	}
	for _, v := range plan.DeleteVariants {
		if v.StoragePath == variantPath {
			t.Fatalf("sibling-pinned variant %s landed in DeleteVariants (H2 data loss)", variantPath)
		}
	}
	kept := false
	for _, v := range plan.KeepVariants {
		if v.StoragePath == variantPath {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("KeepVariants = %+v, want it to contain %s", plan.KeepVariants, variantPath)
	}

	if err := ExecuteGC(primary, plan, opts); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(featurePath, "web", "node_modules", "dep.js"))
	if err != nil {
		t.Fatalf("sibling's variant content gone after gc from primary: %v", err)
	}
	if string(got) != "payload\n" {
		t.Errorf("variant content = %q, want %q", got, "payload\n")
	}
}

// TestGCPlanRealDirAndDanglingLinkAreNotPins pins the negative half
// of the pin definition through the full plan: a workspace path that
// is a REAL directory (the shape a detached copy leaves behind) or a
// DANGLING symlink does not pin the variant, so with nothing else
// referencing it the variant lands in DeleteVariants.
func TestGCPlanRealDirAndDanglingLinkAreNotPins(t *testing.T) {
	cases := []struct {
		name     string
		sabotage func(t *testing.T, wsPath string)
	}{
		{
			name: "real directory replaces the symlink",
			sabotage: func(t *testing.T, wsPath string) {
				if err := os.MkdirAll(wsPath, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(wsPath, "local.txt"), "independent copy\n")
			},
		},
		{
			name: "dangling symlink replaces the symlink",
			sabotage: func(t *testing.T, wsPath string) {
				if err := os.Symlink(filepath.Join(t.TempDir(), "never-exists"), wsPath); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepoWithHead(t, map[string]string{
				".wrk.yml": "resources:\n  - name: data\n    path: data-dir\n",
			})
			storage := storageIn(t, repo.Root)
			opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
			writeFile(t, filepath.Join(repo.Root, "data-dir", "payload.txt"), "x\n")
			if err := Link(repo, opts); err != nil {
				t.Fatalf("Link: %v", err)
			}

			wsPath := filepath.Join(repo.Root, "data-dir")
			if err := os.Remove(wsPath); err != nil {
				t.Fatalf("removing workspace symlink: %v", err)
			}
			tc.sabotage(t, wsPath)

			plan, err := BuildGCPlan(repo, opts)
			if err != nil {
				t.Fatalf("BuildGCPlan: %v", err)
			}
			variantPath := filepath.Join(storage, repo.RepositoryID, "data-dir")
			for _, v := range plan.KeepVariants {
				if v.StoragePath == variantPath {
					t.Fatalf("unpinned variant %s landed in KeepVariants", variantPath)
				}
			}
			if len(plan.DeleteVariants) != 1 || plan.DeleteVariants[0].StoragePath != variantPath {
				t.Fatalf("DeleteVariants = %+v, want exactly [%s]", plan.DeleteVariants, variantPath)
			}
		})
	}
}
