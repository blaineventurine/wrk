package workspace

import (
	"os"
	"path/filepath"
)

// Inspect inspects the current filesystem state for a resource.
func Inspect(
	workspacePath string,
	sharedPath string,
) (State, error) {
	var state State

	info, err := os.Lstat(workspacePath)

	switch {
	case err == nil:
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			state.WorkspaceSymlink = true

			target, err := filepath.EvalSymlinks(workspacePath)
			if err == nil {
				state.WorkspaceTarget = target
			}

		case info.IsDir():
			state.WorkspaceDirectory = true
			state.WorkspaceExists = true

		default:
			state.WorkspaceExists = true
		}

	case os.IsNotExist(err):
		// Missing is fine.

	default:
		return state, err
	}

	if _, err := os.Stat(sharedPath); err == nil {
		state.SharedExists = true
	} else if !os.IsNotExist(err) {
		return state, err
	}

	return state, nil
}
