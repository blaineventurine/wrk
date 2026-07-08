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

// detectGhosts returns the roots of worktrees git still tracks whose
// working directory has vanished. The porcelain listing tags any
// such record with `prunable`; we filter to those and hand back the
// worktree paths so callers can report and reconcile them without a
// second git invocation.
func (gitBackend) detectGhosts(root string) ([]string, error) {
	out, err := capture(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	return parsePrunableWorktrees(out), nil
}

// pruneGhosts collects the ghost roots first (so the return value is
// stable) then runs `git worktree prune`, which is git's supported
// path for garbage-collecting metadata for missing worktrees. Both
// calls surface the underlying failure verbatim; a successful prune
// leaves stdout empty and needs no parsing.
func (gitBackend) pruneGhosts(root string) ([]string, error) {
	ghosts, err := (gitBackend{}).detectGhosts(root)
	if err != nil {
		return nil, err
	}

	if _, err := capture(root, "git", "worktree", "prune"); err != nil {
		return nil, err
	}

	return ghosts, nil
}

// removeWorkspace tears down the worktree at target via `git
// worktree remove`. If target is not among the live worktrees the
// call succeeds silently — a user who rm -rf'd the directory
// out-of-band should be able to invoke this without seeing a
// failure, and the metadata cleanup is `wrk gc`'s job via
// pruneGhosts. Both sides of the path comparison are canonicalized
// so macOS's /private vs /var symlink pair doesn't miss a match.
func (gitBackend) removeWorkspace(root, target string, force bool) error {
	out, err := capture(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}

	canonTarget := canonicalize(target)
	found := false
	for _, ws := range parseWorktreePorcelain(out) {
		if canonicalize(ws) == canonTarget {
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	// "--" separates options from the (absolute) target path so a
	// destination beginning with "-" cannot be reparsed as a flag.
	args = append(args, "--", target)
	return passthrough(root, "git", args...)
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

// parsePrunableWorktrees returns the worktree paths git has marked
// `prunable` in `git worktree list --porcelain` output — the mirror
// of parseWorktreePorcelain, which drops those same records. Bare
// records are skipped because they have no working tree to ghost.
// Returns an empty (non-nil) slice on a clean listing so the backend
// contract (`[]string{}, nil`) is preserved even when no ghosts exist.
func parsePrunableWorktrees(out string) []string {
	ghosts := make([]string, 0)

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

		if bare || !prunable || path == "" {
			continue
		}
		ghosts = append(ghosts, path)
	}

	return ghosts
}
