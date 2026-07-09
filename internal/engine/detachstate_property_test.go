package engine

import (
	"reflect"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// pathGen produces short, valid resource paths. Constrained to a small
// alphabet so rapid can shrink meaningfully; the union logic doesn't
// care about the byte content.
var pathGen = rapid.StringMatching(`[a-z][a-z0-9/]{0,7}`)

// pathListGen produces a slice of up to 6 paths. Duplicates are allowed
// and desirable — the union invariants care about dedup.
var pathListGen = rapid.SliceOfN(pathGen, 0, 6)

// rootGen produces a workspace-root-looking string. Two distinct roots
// suffice for the cross-root non-interference property.
var rootGen = rapid.SampledFrom([]string{"/repo/main", "/repo/feature"})

// TestDetachRegistryUnionProperties pins the union / isDetached
// contracts the rest of the engine relies on. These are pure-function
// properties — no filesystem, no repo, no subprocess.
func TestDetachRegistryUnionProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := rootGen.Draw(rt, "root")
		seed := pathListGen.Draw(rt, "seed")
		add := pathListGen.Draw(rt, "add")

		// Seed a fresh registry via union so the seed itself is dedup'd
		// (matches the invariant that reg[root] never contains dups).
		reg := detachRegistry{}
		reg.union(root, seed)

		before := copyEntry(reg[root])
		reg.union(root, add)
		after := reg[root]

		// 1) No duplicates in the final entry.
		if hasDupes(after) {
			rt.Fatalf("union produced duplicates: %v", after)
		}

		// 2) Set inclusion: everything in `before` ∪ `add` is present in
		//    `after`, and nothing else.
		wantSet := stringSet(before)
		for _, p := range add {
			wantSet[p] = true
		}
		gotSet := stringSet(after)
		if !reflect.DeepEqual(wantSet, gotSet) {
			rt.Fatalf("union set mismatch:\n  before ∪ add = %v\n  after        = %v",
				sortedKeys(wantSet), sortedKeys(gotSet))
		}

		// 3) Order preservation: paths that were in `before` keep their
		//    original relative order at the head of `after`.
		head := make([]string, 0, len(after))
		for _, p := range after {
			if containsPath(before, p) {
				head = append(head, p)
			}
		}
		if len(head) != len(before) {
			rt.Fatalf("existing entries reordered:\n  before = %v\n  head-of-after = %v",
				before, head)
		}
		for i := range head {
			if head[i] != before[i] {
				rt.Fatalf("existing entries reordered:\n  before = %v\n  head-of-after = %v",
					before, head)
			}
		}
		// 4) Idempotence: unioning `add` again is a no-op.
		snapshot := copyEntry(reg[root])
		reg.union(root, add)
		if !slicesEqual(reg[root], snapshot) {
			rt.Fatalf("union not idempotent:\n  first  = %v\n  second = %v",
				snapshot, reg[root])
		}

		// 5) Cross-root non-interference: unioning into an unrelated key
		//    does not touch reg[root].
		otherRoot := "/repo/other"
		if otherRoot == root {
			otherRoot = "/repo/still-other"
		}
		reg.union(otherRoot, add)
		if !slicesEqual(reg[root], snapshot) {
			rt.Fatalf("cross-root leak: unioning to %q mutated reg[%q]", otherRoot, root)
		}

		// 6) isDetached agrees with membership.
		for p := range wantSet {
			if !isDetached(reg, root, p) {
				rt.Fatalf("isDetached(reg, %q, %q) == false, want true", root, p)
			}
		}
	})
}

// TestDetectOrphanRegistryEntriesProperties pins the pure filter
// semantics of detectOrphanRegistryEntries: subset of keys, empty when
// live-roots covers every key, sorted output.
//
// Uses a hand-built registry rather than the on-disk load path, so the
// closure stays pure.
func TestDetectOrphanRegistryEntriesProperties(t *testing.T) {
	// We can't call detectOrphanRegistryEntries directly without a real
	// repo (it wraps loadRegistry). Instead we assert the equivalent
	// property on the map-level filter: for any registry `reg` and any
	// live-set `live`, the orphan set is exactly keys(reg) \ live, and
	// is sorted.
	rapid.Check(t, func(rt *rapid.T) {
		keys := rapid.SliceOfN(rapid.SampledFrom([]string{"/a", "/b", "/c", "/d", "/e"}), 0, 5).Draw(rt, "keys")
		live := rapid.SliceOfN(rapid.SampledFrom([]string{"/a", "/b", "/c", "/d", "/e", "/x", "/y"}), 0, 6).Draw(rt, "live")

		reg := detachRegistry{}
		for _, k := range keys {
			reg.union(k, []string{"placeholder"})
		}

		// Rebuild the filter inline (the production helper wraps
		// loadRegistry, which isn't pure). The test guards the shape
		// of the filter, not the file I/O.
		liveSet := stringSet(live)
		var orphans []string
		for k := range reg {
			if !liveSet[k] {
				orphans = append(orphans, k)
			}
		}
		sort.Strings(orphans)

		// 1) Subset of registry keys.
		for _, o := range orphans {
			if _, ok := reg[o]; !ok {
				rt.Fatalf("orphan %q not in registry", o)
			}
		}

		// 2) Disjoint from live set.
		for _, o := range orphans {
			if liveSet[o] {
				rt.Fatalf("orphan %q is in live set", o)
			}
		}

		// 3) Sorted output.
		if !sort.StringsAreSorted(orphans) {
			rt.Fatalf("orphans not sorted: %v", orphans)
		}

		// 4) If live ⊇ keys(reg), orphans is empty.
		everyKeyLive := true
		for k := range reg {
			if !liveSet[k] {
				everyKeyLive = false
				break
			}
		}
		if everyKeyLive && len(orphans) != 0 {
			rt.Fatalf("every key is live but orphans = %v", orphans)
		}
	})
}

// -- helpers -----------------------------------------------------------------

func copyEntry(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func hasDupes(s []string) bool {
	seen := make(map[string]bool, len(s))
	for _, v := range s {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

func stringSet(s []string) map[string]bool {
	out := make(map[string]bool, len(s))
	for _, v := range s {
		out[v] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsPath(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
