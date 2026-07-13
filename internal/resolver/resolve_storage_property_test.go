package resolver

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/blaineventurine/wrk/internal/config"
)

// cleanEntryPool is the universe of legitimate resource names the
// property test seeds on either side of the union.
var cleanEntryPool = [...]string{
	"alpha", "beta", "gamma", "delta", "epsilon", "zeta",
}

// bookkeepingEntryPool covers every executor scratch suffix; entries
// exist ONLY on the storage side, where the resolver must drop them.
var bookkeepingEntryPool = [...]string{
	"alpha.wrk-lock",
	"beta.wrk-tmp",
	"gamma.wrk-backup",
	"delta.wrk-deleting",
	"epsilon.wrk-forgetting",
	"zeta.wrk-provisioning",
}

// TestResolveWithStorageProperty drives ResolveWithStorage with random
// subsets of entries seeded under the workspace root and the storage
// resource root (the storage side additionally salted with bookkeeping
// entries), then asserts the full result contract in one shot:
//
//	RelativePaths == sorted(union(workspace, storage \ bookkeeping))
//
// which simultaneously proves membership in the input union, absence
// of duplicates, deterministic sorted order, and bookkeeping
// filtering. Each instance must also be workspace-anchored:
// WorkspacePath == Join(root, RelativePath).
func TestResolveWithStorageProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		storageRoot := t.TempDir()

		seed := func(base, name, label string) {
			path := filepath.Join(base, name)
			if rapid.Bool().Draw(rt, label+"-dir") {
				if err := os.MkdirAll(path, 0o755); err != nil {
					rt.Fatalf("mkdir %s: %v", path, err)
				}
				return
			}
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				rt.Fatalf("write %s: %v", path, err)
			}
		}

		union := make(map[string]bool)

		for _, name := range cleanEntryPool {
			if rapid.Bool().Draw(rt, "ws-"+name) {
				seed(root, name, "ws-"+name)
				union[name] = true
			}
			if rapid.Bool().Draw(rt, "stor-"+name) {
				seed(storageRoot, name, "stor-"+name)
				union[name] = true
			}
		}
		for _, name := range bookkeepingEntryPool {
			if rapid.Bool().Draw(rt, "book-"+name) {
				seed(storageRoot, name, "book-"+name)
				// Deliberately NOT added to union: bookkeeping
				// entries must never become instances.
			}
		}

		expected := make([]string, 0, len(union))
		for name := range union {
			expected = append(expected, name)
		}
		sort.Strings(expected)

		instances, err := ResolveWithStorage(root, storageRoot, config.Resource{
			Name: "prop",
			Path: "*",
		})
		if err != nil {
			rt.Fatalf("ResolveWithStorage: %v", err)
		}

		got := make([]string, 0, len(instances))
		for _, inst := range instances {
			rel := inst.RelativePath
			got = append(got, rel)

			// No seeded workspace entry carries a bookkeeping suffix,
			// so any suffixed instance is a storage-side filter leak.
			for _, suffix := range bookkeepingSuffixes {
				if strings.HasSuffix(rel, suffix) {
					rt.Fatalf("bookkeeping entry %q surfaced as an instance", rel)
				}
			}
			if want := filepath.Join(root, filepath.FromSlash(rel)); inst.WorkspacePath != want {
				rt.Fatalf("instance %q: WorkspacePath = %q, want %q",
					rel, inst.WorkspacePath, want)
			}
		}

		if len(got) != len(expected) {
			rt.Fatalf("got %v, want %v", got, expected)
		}
		for i := range got {
			if got[i] != expected[i] {
				rt.Fatalf("got %v, want %v (mismatch at %d)", got, expected, i)
			}
		}
	})
}
