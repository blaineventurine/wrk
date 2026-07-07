package repository

import (
	"path/filepath"
	"strings"
)

type gitBackend struct{}

func (gitBackend) kind() VCS { return Git }

func (gitBackend) commonDir(root string) (string, error) {
	out, err := capture(root, "git", "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}

	path := strings.TrimSpace(out)

	if filepath.IsAbs(path) {
		return path, nil
	}

	return filepath.Join(root, path), nil
}

func (gitBackend) createWorkspace(root, dest string) error {
	return passthrough(root, "git", "worktree", "add", dest)
}

func (gitBackend) workspaces(root string) ([]string, error) {
	out, err := capture(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var paths []string

	for _, line := range strings.Split(out, "\n") {
		// Each record begins with "worktree <path>".
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, strings.TrimSpace(rest))
		}
	}

	return paths, nil
}
