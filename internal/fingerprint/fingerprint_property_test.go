package fingerprint

import (
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

// pathGen produces short valid filenames (no slashes, no dots).
var pathGen = rapid.StringMatching(`[a-z][a-z0-9]{0,6}`)

// contentGen produces up to 64 bytes of arbitrary content, including
// the pathological byte sequence "MISSING" that the domain-separator
// tag is designed to disambiguate.
var contentGen = rapid.SliceOfN(rapid.Byte(), 0, 64)

// TestFingerprintDeterminism pins the invariant that the fingerprint
// of an unchanged file tree is byte-for-byte identical across calls.
// A regression that folded time, filesystem identity, or process
// state into the hash would break this immediately.
func TestFingerprintDeterminism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		files := generateFiles(t, rt, root, "det")

		first, err := Fingerprint(root, absPaths(root, files)...)
		if err != nil {
			rt.Fatalf("Fingerprint #1: %v", err)
		}
		second, err := Fingerprint(root, absPaths(root, files)...)
		if err != nil {
			rt.Fatalf("Fingerprint #2: %v", err)
		}
		if first != second {
			rt.Fatalf("determinism violated: %q != %q (files=%+v)", first, second, files)
		}

		if len(first) != Length {
			rt.Fatalf("fingerprint length = %d, want %d (%q)", len(first), Length, first)
		}
		for i := range len(first) {
			if !isLowerHex(first[i]) {
				rt.Fatalf("non-hex byte at index %d in %q", i, first)
			}
		}
	})
}

// TestFingerprintOrderIndependence pins the invariant that the caller's
// slice ordering doesn't affect the hash. The internal sort is a hidden
// contract the resolver depends on so users who list fingerprint inputs
// in any order get the same variant.
func TestFingerprintOrderIndependence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		files := generateFiles(t, rt, root, "ord")

		paths := absPaths(root, files)
		if len(paths) < 2 {
			return
		}

		forward, err := Fingerprint(root, paths...)
		if err != nil {
			rt.Fatalf("Fingerprint forward: %v", err)
		}

		reversed := make([]string, len(paths))
		for i, p := range paths {
			reversed[len(paths)-1-i] = p
		}
		reverse, err := Fingerprint(root, reversed...)
		if err != nil {
			rt.Fatalf("Fingerprint reverse: %v", err)
		}

		if forward != reverse {
			rt.Fatalf("order-independence violated: forward=%q reverse=%q paths=%v",
				forward, reverse, paths)
		}
	})
}

// TestFingerprintContentSensitivity pins the invariant that changing a
// file's contents changes the fingerprint. A regression that skipped
// content on a fast-path would silently hand out stale variants.
func TestFingerprintContentSensitivity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		files := generateFiles(t, rt, root, "sens")
		if len(files) == 0 {
			return
		}

		before, err := Fingerprint(root, absPaths(root, files)...)
		if err != nil {
			rt.Fatalf("Fingerprint before: %v", err)
		}

		var target string
		for _, f := range files {
			if f.exists {
				target = filepath.Join(root, f.name)
				break
			}
		}
		if target == "" {
			return
		}

		data, err := os.ReadFile(target)
		if err != nil {
			rt.Fatalf("read target: %v", err)
		}
		mutated := append(data, 0xff)
		if err := os.WriteFile(target, mutated, 0o644); err != nil {
			rt.Fatalf("write mutated: %v", err)
		}

		after, err := Fingerprint(root, absPaths(root, files)...)
		if err != nil {
			rt.Fatalf("Fingerprint after: %v", err)
		}

		if before == after {
			rt.Fatalf("content sensitivity violated: same fingerprint %q after mutating %s",
				before, target)
		}
	})
}

// TestFingerprintExistenceSensitivity pins the domain-separator tag:
// a present file with contents "MISSING" produces a different
// fingerprint than a missing file at the same path.
func TestFingerprintExistenceSensitivity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		name := pathGen.Draw(rt, "name")
		path := filepath.Join(root, name)

		if err := os.WriteFile(path, []byte("MISSING"), 0o644); err != nil {
			rt.Fatalf("write MISSING file: %v", err)
		}
		present, err := Fingerprint(root, path)
		if err != nil {
			rt.Fatalf("Fingerprint present: %v", err)
		}

		if err := os.Remove(path); err != nil {
			rt.Fatalf("remove: %v", err)
		}
		absent, err := Fingerprint(root, path)
		if err != nil {
			rt.Fatalf("Fingerprint absent: %v", err)
		}

		if present == absent {
			rt.Fatalf("existence-sensitivity violated: present-with-MISSING == absent (%q)",
				present)
		}
	})
}

// TestFingerprintRepoIndependence pins the invariant that fingerprints
// depend on repo-relative paths and file contents, NOT on the absolute
// path of the repository root. Two workspaces of the same repo must
// compute identical fingerprints for identical file contents.
func TestFingerprintRepoIndependence(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		rootA := t.TempDir()
		rootB := t.TempDir()

		files := generateFiles(t, rt, rootA, "repo")
		mirrorFiles(t, files, rootB)

		fpA, err := Fingerprint(rootA, absPaths(rootA, files)...)
		if err != nil {
			rt.Fatalf("Fingerprint A: %v", err)
		}
		fpB, err := Fingerprint(rootB, absPaths(rootB, files)...)
		if err != nil {
			rt.Fatalf("Fingerprint B: %v", err)
		}

		if fpA != fpB {
			rt.Fatalf("repo-independence violated: %q != %q\n  rootA=%s\n  rootB=%s",
				fpA, fpB, rootA, rootB)
		}
	})
}

// --- generators and helpers ---

type fileSpec struct {
	name    string
	content []byte
	exists  bool
}

func generateFiles(t *testing.T, rt *rapid.T, root, label string) []fileSpec {
	t.Helper()
	count := rapid.IntRange(0, 4).Draw(rt, label+"-count")

	seen := map[string]bool{}
	files := make([]fileSpec, 0, count)
	for range count {
		var name string
		for range 4 {
			candidate := pathGen.Draw(rt, label+"-name")
			if !seen[candidate] {
				name = candidate
				break
			}
		}
		if name == "" {
			continue
		}
		seen[name] = true

		content := contentGen.Draw(rt, label+"-content")
		exists := rapid.Bool().Draw(rt, label+"-exists")
		if exists {
			if err := os.WriteFile(filepath.Join(root, name), content, 0o644); err != nil {
				t.Fatalf("materialize %s: %v", name, err)
			}
		}
		files = append(files, fileSpec{name: name, content: content, exists: exists})
	}
	return files
}

func mirrorFiles(t *testing.T, files []fileSpec, dest string) {
	t.Helper()
	for _, f := range files {
		if !f.exists {
			continue
		}
		if err := os.WriteFile(filepath.Join(dest, f.name), f.content, 0o644); err != nil {
			t.Fatalf("mirror %s: %v", f.name, err)
		}
	}
}

func absPaths(root string, files []fileSpec) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, filepath.Join(root, f.name))
	}
	return out
}

func isLowerHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}
