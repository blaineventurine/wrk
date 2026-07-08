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
	// "--" separates options from the (absolute) destination path so a
	// destination beginning with "-" cannot be reparsed as a flag.
	// resolveDestination already yields an absolute path, but the
	// separator is defensive: cheap, and pins the invariant in code.
	return passthrough(root, "git", "worktree", "add", "--", dest)
}

func (gitBackend) workspaces(root string) ([]string, error) {
	out, err := capture(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	return parseWorktreePorcelain(out), nil
}

// parseWorktreePorcelain extracts the roots of live worktrees from
// `git worktree list --porcelain` output.
//
// Grammar (from git-worktree(1), --porcelain section):
//
//   - Records are separated by blank lines.
//   - Each record is a sequence of "key value" lines and key-only
//     sentinels. The set of keys we care about:
//   - "worktree <path>" — every live and prunable worktree record;
//     absent on a bare primary.
//   - "bare"            — sentinel: this record is the bare primary,
//     which has no working tree.
//   - "prunable [<reason>]"
//     — sentinel: this worktree's directory is
//     missing / broken and will fail Detect if used.
//
// We return only the live-worktree paths: records tagged `bare` yield
// no path (they have no working tree), and records tagged `prunable`
// are dropped so callers don't try to walk into a directory that no
// longer exists.
func parseWorktreePorcelain(out string) []string {
	var paths []string

	for _, record := range strings.Split(out, "\n\n") {
		var (
			path     string
			bare     bool
			prunable bool
		)

		for _, line := range strings.Split(record, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}

			key, value, hasValue := strings.Cut(line, " ")
			switch key {
			case "worktree":
				if hasValue {
					path = strings.TrimSpace(value)
				}
			case "bare":
				bare = true
			case "prunable":
				prunable = true
			}
		}

		if bare || prunable || path == "" {
			continue
		}
		paths = append(paths, path)
	}

	return paths
}
