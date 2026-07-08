package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateWorkspace creates a new workspace/worktree at destination and
// returns the repository rooted there.
//
// The destination is resolved by resolveDestination: bare names (no
// path separator, not "." or "..") become siblings of the current
// workspace root by prepending "..", so `wrk new feature` from
// /proj/main lands at /proj/feature. Explicit relative paths and
// absolute paths are left alone.
//
// The resolved destination must also not sit inside any existing
// workspace of this repository — nested worktrees confuse both git and
// jj, and wrk's shared-storage links assume workspace roots are
// siblings, not parents/children of one another.
func (r *Repository) CreateWorkspace(destination string) (*Repository, error) {
	dest := resolveDestination(r.Root, destination)

	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("destination already exists: %s", dest)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	workspaces, err := r.Workspaces()
	if err != nil {
		return nil, err
	}
	if ws, err := containingWorkspace(dest, workspaces); err != nil {
		return nil, err
	} else if ws != "" {
		return nil, fmt.Errorf(
			"destination %s is inside existing workspace %s; "+
				"new workspaces must be created outside any existing one "+
				"(wrk defaults bare names to a sibling of the current workspace)",
			dest, ws,
		)
	}

	if err := r.backend.createWorkspace(r.Root, dest); err != nil {
		return nil, err
	}

	return Detect(dest, r.VCS())
}

// Workspaces returns the roots of every live workspace/worktree for this
// repository, including the primary one.
func (r *Repository) Workspaces() ([]string, error) {
	return r.backend.workspaces(r.Root)
}

// resolveDestination applies wrk's sibling-default policy so that
// `wrk new feature` (a bare name) lands next to the current workspace
// instead of inside it. Anything with a path separator or a leading
// dot is treated literally — explicit paths are the user's call.
func resolveDestination(root, destination string) string {
	if filepath.IsAbs(destination) {
		return filepath.Clean(destination)
	}
	if isBareName(destination) {
		return filepath.Clean(filepath.Join(root, "..", destination))
	}
	return filepath.Clean(filepath.Join(root, destination))
}

// isBareName reports whether name is a simple identifier suitable for
// sibling-defaulting — no path separators, not the special . / ..
// entries. Empty strings are not bare names either (callers should
// reject them upstream, but the check keeps this helper total).
func isBareName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

// containingWorkspace returns the first workspace root in workspaces
// that equals or contains dest, or "" if dest sits outside every one.
//
// Both sides are canonicalized (symlinks resolved) before comparison
// because VCS tooling — `git worktree list`, `jj workspace list` —
// reports canonical paths, whereas dest is built from the caller's
// r.Root and may still traverse a symlink (macOS /var → /private/var
// is the common case). Without canonicalization, filepath.Rel between
// the two forms wanders through "../.." and false-negatives every
// nesting check.
func containingWorkspace(dest string, workspaces []string) (string, error) {
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	canonDest := canonicalize(absDest)

	for _, ws := range workspaces {
		absWS, err := filepath.Abs(ws)
		if err != nil {
			return "", err
		}
		canonWS := canonicalize(absWS)

		rel, err := filepath.Rel(canonWS, canonDest)
		if err != nil {
			// Different volumes on Windows, etc. — treat as unrelated.
			continue
		}

		// Rel returns "." for equal paths, and a path whose first
		// component is ".." when dest is outside ws.
		if rel == "." {
			return ws, nil
		}
		if parts := strings.Split(rel, string(filepath.Separator)); parts[0] != ".." {
			return ws, nil
		}
	}

	return "", nil
}

// canonicalize returns the symlink-resolved form of path, walking up
// to the deepest existing ancestor when path itself does not yet
// exist (as is the case for a to-be-created workspace destination).
// If no ancestor resolves — e.g. path is entirely fictional — the
// input is returned unchanged.
func canonicalize(path string) string {
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
