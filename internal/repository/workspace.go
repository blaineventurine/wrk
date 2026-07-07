package repository

import (
	"fmt"
	"os"
	"path/filepath"
)

// CreateWorkspace creates a new workspace/worktree at destination and
// returns the repository rooted there.
func (r *Repository) CreateWorkspace(destination string) (*Repository, error) {
	dest := destination
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(r.Root, dest)
	}
	dest = filepath.Clean(dest)

	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("destination already exists: %s", dest)
	} else if !os.IsNotExist(err) {
		return nil, err
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
