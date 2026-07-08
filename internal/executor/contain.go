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

// canonicalizeExecutor is the executor's copy of the ancestor-tolerant
// canonicalization loop used by internal/repository. Kept private and
// duplicated to avoid a cyclic import; keep behaviour identical.
func canonicalizeExecutor(path string) string {
	var suffix []string
	current := path
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(append([]string{resolved}, suffix...)...)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}
