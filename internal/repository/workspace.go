package repository

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func (r *Repository) CreateWorkspace(
	destination string,
) (*Repository, error) {
	workspace := destination

	if !filepath.IsAbs(workspace) {
		workspace = filepath.Join(
			r.Root,
			workspace,
		)
	}

	workspace = filepath.Clean(workspace)

	if _, err := os.Stat(workspace); err == nil {
		return nil, fmt.Errorf(
			"destination already exists: %s",
			workspace,
		)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	var cmd *exec.Cmd

	switch r.VCS {
	case JJ:
		cmd = exec.Command(
			"jj",
			"workspace",
			"add",
			workspace,
		)

	case Git:
		cmd = exec.Command(
			"git",
			"worktree",
			"add",
			workspace,
		)

	default:
		return nil, fmt.Errorf(
			"unsupported VCS %q",
			r.VCS,
		)
	}

	cmd.Dir = r.Root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return Detect(
		workspace,
		r.VCS,
	)
}
