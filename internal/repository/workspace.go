package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateWorkspace creates a workspace/worktree at destination and
// returns a Repository rooted there. Bare names become siblings of
// the current workspace; nesting inside an existing workspace is
// refused.
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

// RemoveWorkspace tears down the workspace/worktree at target.
// force passes through to the VCS command (git worktree remove
// --force); jj workspace forget has no --force flag so force is a
// no-op on the jj backend. Idempotent: if the workspace is already
// absent from VCS metadata, returns nil.
//
// target is canonicalized with filepath.Abs — NOT EvalSymlinks —
// because the caller passed a specific path; walking through
// symlinks might land on a different workspace than intended.
func (r *Repository) RemoveWorkspace(target string, force bool) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	return r.backend.removeWorkspace(r.Root, abs, force)
}

// UncommittedCount returns the number of files with uncommitted
// changes in target. For git this is `git status --porcelain` line
// count (tracked-modified, staged, or untracked); for jj it is
// `jj diff --summary` line count against the @ change's parent.
// Read-only. A probe failure (missing metadata, VCS binary not on
// PATH, permission denied) surfaces as the returned error; the
// caller decides whether to swallow it — the remove-plan builder
// does, because a plan without an uncommitted-changes signal is
// still useful and the executor sees real failures at commit time.
//
// target is canonicalized with filepath.Abs — NOT EvalSymlinks —
// so callers passing a specific path do not accidentally probe a
// different workspace via a symlink.
func (r *Repository) UncommittedCount(target string) (int, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return 0, err
	}
	return r.backend.uncommittedCount(abs)
}

// ResolveDestination applies the sibling-default policy and refuses
// destinations that already exist or nest inside a live workspace.
// Read-only; shared with CreateWorkspace so preflight is single-source.
func (r *Repository) ResolveDestination(destination string) (string, error) {
	// Empty / "." / ".." collapse to r.Root or its parent; reject them
	// with a clearer error than "destination already exists: <root>".
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

// DetectGhosts returns absolute workspace roots that VCS metadata
// still references but whose working directory is missing. Read-only:
// callers (notably `wrk gc`) inspect the result and decide whether to
// reconcile via PruneGhosts. Returns an empty (non-nil) slice on a
// clean repository.
func (r *Repository) DetectGhosts() ([]string, error) {
	return r.backend.detectGhosts(r.Root)
}

// PruneGhosts detects ghost workspaces (as DetectGhosts does) and
// cleans them from the underlying VCS's metadata. Returns the roots
// pruned so callers can surface them in output. Returns an empty
// (non-nil) slice when there was nothing to prune.
func (r *Repository) PruneGhosts() ([]string, error) {
	return r.backend.pruneGhosts(r.Root)
}

// resolveDestination applies the sibling-default policy: a bare name
// becomes ../<name>; paths with a separator or leading dot pass
// through untouched.
func resolveDestination(root, destination string) string {
	if filepath.IsAbs(destination) {
		return filepath.Clean(destination)
	}
	if isBareName(destination) {
		return filepath.Clean(filepath.Join(root, "..", destination))
	}
	return filepath.Clean(filepath.Join(root, destination))
}

// isBareName reports whether name is a simple identifier (no
// separators, not "." or ".."). Empty strings are also not bare.
func isBareName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

// containingWorkspace returns the first workspace root that equals
// or contains dest, or "" if dest sits outside every one. Both sides
// are canonicalized so a symlinked temp dir doesn't false-negative.
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
