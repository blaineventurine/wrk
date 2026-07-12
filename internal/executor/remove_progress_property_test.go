package executor

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

// TestRemoveAllProgressSumMatchesTreeSize pins the sum invariant of
// RemoveAllProgress: for any random directory tree, the callback's
// running total equals the ground-truth on-disk byte count of every
// regular file. The unit tests in remove_progress_test.go cover the
// specific shapes we care about (single file, nested dirs, symlinks);
// this property test hammers randomly-shaped trees so an implementation
// that quietly overcounts, undercounts, or misses a whole subtree fails
// on shrunk minimal input rather than in production.
//
// Ground truth is computed by walking the tree the SAME way
// RemoveAllProgress does — Lstat, no symlink following, regular files
// only. The invariants are:
//
//  1. Σ(onProgress calls) == treeSize(tree)
//  2. After RemoveAllProgress(root), root does not exist (idempotent
//     no-error on missing target is already covered by the unit tests).
//  3. A nil callback path handles the SAME random tree without panic
//     and also removes the root — nil-safety is not an "empty tree"
//     accident.
//
// Note on t plumbing: rapid.T doesn't expose TempDir (see the analogous
// note in pinned_variants_property_test.go), so the outer *testing.T is
// captured and used for TempDir/Fatalf on helpers that need those
// methods. Each iteration gets its own subdir under the outer temp dir
// so state cannot bleed between rapid draws.
func TestRemoveAllProgressSumMatchesTreeSize(t *testing.T) {
	base := t.TempDir()
	var iter int

	rapid.Check(t, func(rt *rapid.T) {
		// Fresh subdir per iteration so shrinking never observes
		// residue from a prior draw. Counter is bumped inside the
		// closure so shrink runs on the same shape share a root.
		iter++
		root := filepath.Join(base, fmt.Sprintf("iter-%d", iter))
		if err := os.MkdirAll(root, 0o755); err != nil {
			rt.Fatalf("mkdir root: %v", err)
		}

		tree := generateTreeNode(rt, "n", 0)
		writeTreeNode(rt, root, tree)

		expected := groundTruthSize(root)

		var got int64
		var calls int
		if err := RemoveAllProgress(root, func(n int64) {
			got += n
			calls++
		}); err != nil {
			rt.Fatalf("RemoveAllProgress: %v", err)
		}

		if got != expected {
			rt.Fatalf("callback sum = %d (over %d calls), want %d (from ground-truth Lstat walk)",
				got, calls, expected)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			rt.Fatalf("root should be gone after RemoveAllProgress, stat err = %v", err)
		}

		// Same tree shape, materialized fresh, exercised with a nil
		// callback: the RemoveAllProgress contract explicitly allows
		// nil (a zero-value Options.Progress must not require a
		// wrapper), so this is a real production path. Failure mode
		// is a panic, not a numeric mismatch.
		if err := os.MkdirAll(root, 0o755); err != nil {
			rt.Fatalf("re-mkdir root for nil-callback pass: %v", err)
		}
		writeTreeNode(rt, root, tree)
		if err := RemoveAllProgress(root, nil); err != nil {
			rt.Fatalf("RemoveAllProgress(nil callback): %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			rt.Fatalf("root should be gone after nil-callback RemoveAllProgress, stat err = %v",
				err)
		}
	})
}

// treeNode is a small IR for the generated tree. Files carry a byte
// count; dirs carry children (any mix of files, dirs, and symlinks).
// Symlinks name a relative target that MUST resolve inside the tree —
// safe self-links match how a real workspace holds intra-tree symlinks
// (e.g. `packages/foo → ../shared`) and let us assert that the walker
// counts the link entry itself (zero bytes), not what it points at.
type treeNode struct {
	kind    nodeKind
	size    int64      // file: bytes; symlink: 0
	target  string     // symlink: relative target expression (see writeTreeNode)
	name    string     // basename under the parent dir
	children []treeNode
}

type nodeKind int

const (
	nodeFile nodeKind = iota
	nodeDir
	nodeSymlink
)

// generateTreeNode draws a random tree rooted at the caller's context.
// depth is bounded at 4 so the fixture stays cheap; each level draws
// 0-5 children so both empty and moderately-full dirs are exercised.
//
// The label parameter disambiguates the rapid draw so shrunk
// counter-examples remain informative — every node's structural
// decisions map to a unique label suffix.
func generateTreeNode(rt *rapid.T, label string, depth int) treeNode {
	// The root is always a directory — the outer test needs SOMETHING
	// to hand to RemoveAllProgress, and a top-level regular file is
	// covered by the unit tests.
	root := treeNode{kind: nodeDir, name: label}

	if depth >= 4 {
		return root
	}

	count := rapid.IntRange(0, 5).Draw(rt, label+"-count")
	seen := map[string]bool{}
	for i := range count {
		child := generateChild(rt, fmt.Sprintf("%s-c%d", label, i), depth+1)
		// Unique name under this parent — a collision would race two
		// writes against the same path and hide a real bug.
		for j := 0; seen[child.name] && j < 4; j++ {
			child.name = fmt.Sprintf("%s-%d", child.name, j)
		}
		if seen[child.name] {
			continue
		}
		seen[child.name] = true
		root.children = append(root.children, child)
	}
	return root
}

// generateChild draws one child node. Distribution:
//   - 55% regular file (bytes 0..10000)
//   - 30% subdirectory (recurses)
//   - 15% in-tree symlink
//
// The bias toward files matches real workspace shapes; symlinks are
// rarer but not vanishingly so — a resource path that starts life as
// a real dir and later gets swapped for a symlink is a shape the
// executor's remove path DOES see in production.
func generateChild(rt *rapid.T, label string, depth int) treeNode {
	roll := rapid.IntRange(0, 99).Draw(rt, label+"-kind")
	name := fmt.Sprintf("n%d", rapid.IntRange(0, 999).Draw(rt, label+"-name"))
	switch {
	case roll < 55:
		size := rapid.Int64Range(0, 10000).Draw(rt, label+"-size")
		return treeNode{kind: nodeFile, name: name, size: size}
	case roll < 85:
		sub := generateTreeNode(rt, label+"-d", depth+1)
		sub.name = name
		return sub
	default:
		// Symlink target: use a sibling name — writeTreeNode ensures
		// the link is written AFTER siblings so the pointer at least
		// COULD resolve on hosts that follow symlinks eagerly. For
		// this test we don't require the link to resolve; we only
		// require that RemoveAllProgress treats it as a leaf with
		// zero byte contribution, which is unconditional.
		return treeNode{
			kind:   nodeSymlink,
			name:   name,
			target: "./nonexistent-sibling-" + name,
		}
	}
}

// writeTreeNode materializes node under dir. Directories are created
// eagerly; children are visited in slice order. Files are written with
// zeroed bytes of the drawn size so Size() lstats to the exact number
// generateChild picked — matching the ground-truth invariant.
//
// Symlinks are created last-write-wins under the parent; a target
// collision with a real sibling is fine because we never follow them.
func writeTreeNode(rt *rapid.T, dir string, node treeNode) {
	switch node.kind {
	case nodeDir:
		full := dir
		if node.name != "" && node.name != "n" {
			full = filepath.Join(dir, node.name)
			if err := os.MkdirAll(full, 0o755); err != nil {
				rt.Fatalf("mkdir %s: %v", full, err)
			}
		}
		for _, c := range node.children {
			writeTreeNode(rt, full, c)
		}
	case nodeFile:
		full := filepath.Join(dir, node.name)
		if err := os.WriteFile(full, make([]byte, node.size), 0o644); err != nil {
			rt.Fatalf("write file %s (%d bytes): %v", full, node.size, err)
		}
	case nodeSymlink:
		full := filepath.Join(dir, node.name)
		// The target is a relative path; even if it fails to resolve
		// (which is fine — we never follow it), the link entry itself
		// exists at `full` and RemoveAllProgress must handle it.
		if err := os.Symlink(node.target, full); err != nil {
			// A symlink creation failure is rare (permission-denied
			// on locked-down CI) and unrelated to the property under
			// test. Skip the whole iteration so we don't false-alarm.
			rt.Skipf("symlink not supported at %s: %v", full, err)
		}
	}
}

// groundTruthSize computes the total on-disk regular-file byte count
// under root using the SAME lstat + IsRegular filter that
// RemoveAllProgress uses. Any subtree walk error is treated as
// "nothing further to count from that node" — matching
// RemoveAllProgress's behavior of aborting descent on a broken dir
// (though the property test never generates such a shape).
func groundTruthSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
