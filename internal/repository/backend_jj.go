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
	// `jj workspace list` prints one workspace per line as "name: <ids>".
	out, err := capture(root, "jj", "workspace", "list")
	if err != nil {
		return nil, err
	}

	var paths []string

	for _, line := range strings.Split(out, "\n") {
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		// Resolve the name to a filesystem path.
		wsRoot, err := capture(root, "jj", "workspace", "root", "--name", name)
		if err != nil {
			return nil, err
		}

		paths = append(paths, strings.TrimSpace(wsRoot))
	}

	return paths, nil
}
