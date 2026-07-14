package resolver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// FuzzResolveWithStorage hammers ResolveWithStorage with arbitrary
// pattern strings against a fixed read-only fixture. Invariants:
//
//   - never panics; a malformed pattern (filepath.ErrBadPattern) or an
//     escaping non-glob path surfaces as a returned error;
//   - every returned instance stays INSIDE the workspace root (checked
//     via filepath.Rel, independent of the production containment
//     code) and is never the root itself;
//   - RelativePath is the slash-form of that Rel result and
//     WorkspacePath re-joins to it;
//   - results are strictly increasing (sorted, no duplicates);
//   - the storage-only bookkeeping fixture entry never surfaces;
//   - for GLOB patterns, no instance's RelativePath hits
//     config.DisallowedResourcePath: the expansion filter must drop
//     .git / .wrk.yml / reserved-suffix matches on both sides (non-glob
//     literals legitimately bypass the filter).
func FuzzResolveWithStorage(f *testing.F) {
	// Shared fixture, built once; ResolveWithStorage never writes.
	root := f.TempDir()
	storageRoot := filepath.Join(f.TempDir(), "repo-id")

	fixture := map[string][]string{
		root: {
			"afile",
			".env",
			"adir/child",
			"sub/node_modules",
			"ws.wrk-tmp",  // workspace-side bookkeeping-looking name
			".git/config", // infrastructure: glob expansion must skip
			".wrk.yml",    // infrastructure: glob expansion must skip
		},
		storageRoot: {
			"afile", // overlaps the workspace for dedup pressure
			"storonly",
			"stor.wrk-lock", // must NEVER surface as an instance
			"sub/node_modules",
			"pkg/x/node_modules",
			".git/config", // infrastructure on the storage side too
		},
		// Escape bait one level above the storage subtree.
		filepath.Dir(storageRoot): {"escapee"},
	}
	for base, rels := range fixture {
		for _, rel := range rels {
			path := filepath.Join(base, filepath.FromSlash(rel))
			mkFuzzFixtureFile(f, path)
		}
	}

	seeds := []string{
		"*",
		"a*",
		"afile",
		".env",
		"sub/*",
		"*/node_modules",
		"pkg/*/node_modules",
		"*.wrk-tmp",
		"*.wrk-lock",
		".git*",
		"*/config",
		"../*",
		"../escapee",
		"..",
		".",
		"",
		"?",
		"[",
		"[a-",
		"\\",
		"**",
		"storonly",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		instances, err := ResolveWithStorage(root, storageRoot, config.Resource{
			Name: "fuzz",
			Path: pattern,
		})
		if err != nil {
			// Errors (including filepath.ErrBadPattern and non-glob
			// paths escaping the root) are the accepted failure mode;
			// only a panic or a bad instance is a bug.
			return
		}

		isGlobPattern := isGlob(pattern)
		prev := ""
		for i, inst := range instances {
			rel, rerr := filepath.Rel(root, inst.WorkspacePath)
			if rerr != nil || rel == "." || rel == ".." ||
				strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("pattern %q: instance %q escapes root %q (rel=%q, err=%v)",
					pattern, inst.WorkspacePath, root, rel, rerr)
			}
			if got := filepath.ToSlash(rel); got != inst.RelativePath {
				t.Fatalf("pattern %q: RelativePath = %q, want %q",
					pattern, inst.RelativePath, got)
			}
			if inst.RelativePath == "stor.wrk-lock" {
				t.Fatalf("pattern %q: storage bookkeeping entry surfaced", pattern)
			}
			if isGlobPattern {
				clean := filepath.Clean(filepath.FromSlash(inst.RelativePath))
				if perr := config.DisallowedResourcePath(clean); perr != nil {
					t.Fatalf(
						"pattern %q: glob expansion surfaced disallowed path %q: %v",
						pattern, inst.RelativePath, perr,
					)
				}
			}
			if i > 0 && inst.WorkspacePath <= prev {
				t.Fatalf("pattern %q: results not strictly sorted: %q after %q",
					pattern, inst.WorkspacePath, prev)
			}
			prev = inst.WorkspacePath
		}
	})
}

// mkFuzzFixtureFile creates path (and parents) as an empty file.
func mkFuzzFixtureFile(f *testing.F, path string) {
	f.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.Fatalf("fixture %s: %v", path, err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		f.Fatalf("fixture %s: %v", path, err)
	}
}
