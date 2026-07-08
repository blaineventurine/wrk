package repository

import "strings"

type jjBackend struct{}

func (jjBackend) kind() VCS { return JJ }

func (jjBackend) commonDir(root string) (string, error) {
	out, err := capture(root, "jj", "git", "root")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

func (jjBackend) createWorkspace(root, dest string) error {
	return passthrough(root, "jj", "workspace", "add", dest)
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
