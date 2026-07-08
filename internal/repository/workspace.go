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
	dest, err := r.ResolveDestination(destination)
	if err != nil {
		return nil, err
	}

	if err := r.backend.createWorkspace(r.Root, dest); err != nil {
		return nil, err
	}

	return Detect(dest, r.VCS())
}

// ResolveDestination applies wrk's sibling-default policy, then
// verifies that the destination does not already exist and does not
// sit inside any live workspace of this repository. Returns the
// resolved absolute path.
//
// Read-only: this performs the same preflight as CreateWorkspace but
// creates nothing. It is used by `wrk new --dry-run` and by
// CreateWorkspace itself so the two share a single source of truth.
func (r *Repository) ResolveDestination(destination string) (string, error) {
	// Reject sentinel destinations up front. Without this, the empty
	// string collapses to r.Root and produces the user-hostile
	// "destination already exists: <root>". "." and ".." are handled
	// analogously — they resolve to the current or parent directory,
	// which is either the current workspace itself or something wildly
	// unrelated to what the user asked for.
	trimmed := strings.TrimSpace(destination)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("destination cannot be %q", destination)
	}

	dest := resolveDestination(r.Root, destination)

	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("destination already exists: %s", dest)
	} else if !os.IsNotExist(err) {
		return "", err
	}

	workspaces, err := r.Workspaces()
	if err != nil {
		return "", err
	}
	if ws, err := containingWorkspace(dest, workspaces); err != nil {
		return "", err
	} else if ws != "" {
		return "", fmt.Errorf(
			"destination %s is inside existing workspace %s; "+
				"new workspaces must be created outside any existing one "+
				"(wrk defaults bare names to a sibling of the current workspace)",
			dest, ws,
		)
	}

	return dest, nil
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
// r.Root. Since B4, findRoot canonicalizes at detection time so r.Root
// is already canonical; the canonicalize calls below are defense-in-
// depth for callers that hand-craft a Repository or pass a raw path.
// Without canonicalization, filepath.Rel between the two forms wanders
// through "../.." and false-negatives every nesting check.
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
