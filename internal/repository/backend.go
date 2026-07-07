package repository

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// backend encapsulates all VCS-specific operations. Exactly one
// implementation is selected per repository at detection time, so the
// rest of the package never switches on VCS.
type backend interface {
	kind() VCS

	// commonDir returns the shared metadata directory for the repository,
	// used for identity hashing and repository-local configuration.
	commonDir(root string) (string, error)

	// createWorkspace creates a new workspace/worktree at dest, running
	// from root.
	createWorkspace(root, dest string) error

	// workspaces returns the roots of every live workspace/worktree.
	workspaces(root string) ([]string, error)
}

func backendFor(vcs VCS) (backend, error) {
	switch vcs {
	case Git:
		return gitBackend{}, nil
	case JJ:
		return jjBackend{}, nil
	default:
		return nil, fmt.Errorf("unsupported VCS %q", vcs)
	}
}

// capture runs a command in dir and returns its trimmed stdout.
//
// On failure the error includes the command, working directory, and —
// when captured — the process's stderr output.
func capture(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return "", wrapExecError(name, args, dir, err)
	}
	return string(out), nil
}

// passthrough runs a command in dir, wiring stdio to the process.
//
// stderr from the child is already visible on the user's terminal, so
// the wrapped error only records which command failed and where.
func passthrough(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"%s %s failed (in %s): %w",
			name, strings.Join(args, " "), dir, err,
		)
	}
	return nil
}

// wrapExecError augments an exec error with the command, working
// directory, and — when the underlying error is an *exec.ExitError with
// captured stderr — the process's stderr output.
func wrapExecError(name string, args []string, dir string, err error) error {
	msg := fmt.Sprintf(
		"%s %s failed (in %s): %v",
		name, strings.Join(args, " "), dir, err,
	)

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		if trimmed := strings.TrimSpace(string(exitErr.Stderr)); trimmed != "" {
			msg += ": " + trimmed
		}
	}

	return errors.New(msg)
}
