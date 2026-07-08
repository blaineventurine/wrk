package repository

import (
	"fmt"
	"strings"
)

type jjBackend struct{}

func (jjBackend) kind() VCS { return JJ }

func (jjBackend) commonDir(root string) (string, error) {
	// wrk only supports colocated jj repos: identity, worktree
	// walking, and index awareness all reuse the git plumbing that
	// non-colocated jj checkouts do not expose. `jj git root` fails
	// with an internal message ("error: not a colocated repo") that
	// leaves users guessing; wrap it so the requirement is explicit.
	out, err := capture(root, "jj", "git", "root")
	if err != nil {
		return "", fmt.Errorf(
			"wrk requires jj repositories to be colocated with git (jj/git init --colocate); %w",
			err,
		)
	}

	return strings.TrimSpace(out), nil
}

func (jjBackend) createWorkspace(root, dest string) error {
	// "--" separates options from the (absolute) destination path so a
	// destination beginning with "-" cannot be reparsed as a flag.
	// resolveDestination already yields an absolute path, but the
	// separator is defensive: cheap, and pins the invariant in code.
	return passthrough(root, "jj", "workspace", "add", "--", dest)
}

func (jjBackend) workspaces(root string) ([]string, error) {
	// One process, canonical paths, no name parsing. `self.root()`
	// is the WorkspaceRef method that returns the workspace root
	// path; on jj too old to know it, this call fails loudly rather
	// than silently falling back to the old per-workspace shell-out.
	out, err := capture(
		root,
		"jj", "workspace", "list",
		"--template", `self.root() ++ "\n"`,
	)
	if err != nil {
		return nil, err
	}

	var paths []string

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}

	return paths, nil
}
