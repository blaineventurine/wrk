package executor

import (
	"path/filepath"
	"strings"
)

// containedIn reports whether path (which may not exist yet) resolves
// under root after following symlinks along every ancestor.
//
// It walks up path until it finds an ancestor that exists, resolves
// that ancestor through filepath.EvalSymlinks, then re-appends the
// unresolved suffix. This matches the behaviour of the canonicalize
// helper in internal/repository — keep the two impls in sync mentally;
// they must not diverge.
//
// root MUST already be canonical (symlinks resolved). Callers get it
// from repository.Repository.Root, which is canonicalized on load.
func containedIn(path, root string) (bool, error) {
	if path == "" || root == "" {
		return false, nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}

	canon := canonicalizeExecutor(absPath)

	rel, err := filepath.Rel(root, canon)
	if err != nil {
		return false, nil
	}

	// If rel starts with ".." (or is exactly ".."), canon is outside root.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}

	return true, nil
}

// canonicalizeExecutor resolves symlinks along path's ANCESTORS only,
// leaving the leaf component alone. This is the executor's containment-
// check pass, and every mutating action (Move.Source, Symlink.Link,
// Remove.Path, Detach.Link) may legitimately have a symlink AT the
// leaf — Detach and Symlink exist precisely to replace such a symlink,
// which today points into shared storage OUTSIDE the workspace. If we
// resolved the leaf, every Detach on a fully-linked workspace would
// false-positive.
//
// Ancestor symlinks are still resolved: the C4 escape guard fires when
// a workspace-side symlink (`tools/build → /etc/build`) appears in the
// ancestor chain of a configured resource path.
//
// This intentionally diverges from `internal/repository`.canonicalize,
// which resolves the whole path. Do NOT re-sync the two — the executor
// operates on paths whose leaf may be a symlink by design.
func canonicalizeExecutor(path string) string {
	base := filepath.Base(path)
	parent := filepath.Dir(path)

	// Root-ish input (`/`, `.`) — nothing to walk.
	if parent == path {
		return path
	}

	var suffix []string
	current := parent
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			parts := append([]string{resolved}, suffix...)
			parts = append(parts, base)
			return filepath.Join(parts...)
		}
		next := filepath.Dir(current)
		if next == current {
			return path
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = next
	}
}
