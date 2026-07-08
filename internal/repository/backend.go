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

	// detectGhosts returns the roots of workspaces the VCS still
	// tracks whose working directory is missing on disk. Read-only.
	// Returns an empty slice (not nil) when the repository is clean.
	detectGhosts(root string) ([]string, error)

	// pruneGhosts detects ghost workspaces (as detectGhosts does) and
	// cleans them from the VCS's metadata. Returns the roots pruned so
	// callers can surface them in output. Returns an empty slice (not
	// nil) when the repository is clean.
	pruneGhosts(root string) ([]string, error)

	// removeWorkspace tears down the workspace/worktree at target,
	// running from root. force enables VCS-specific override of
	// safety refusals (`git worktree remove --force`); jj's
	// `workspace forget` has no equivalent flag and treats force as
	// a no-op. Idempotent: if target is not currently tracked as a
	// live workspace, returns nil.
	removeWorkspace(root, target string, force bool) error
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
// Failures wrap the underlying error (usable with errors.Is/As for
// *exec.ExitError, exec.ErrNotFound) and append the child's stderr
// for user context. The child gets a sanitized env (git-dir overrides
// stripped, LC_ALL=C) so parsers see stable output.
func capture(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = sanitizedEnv()

	out, err := cmd.Output()
	if err != nil {
		return "", wrapExecError(name, args, dir, err)
	}
	return string(out), nil
}

// passthrough runs a command in dir with stdio wired to the process,
// for user-facing commands (git worktree add, jj workspace add).
// stdin is passed through so interactive prompts work; stderr is
// already visible so wrapExecError only records what failed and where.
// Unlike capture, the child inherits the parent's full environment.
func passthrough(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
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
// directory, and (for *exec.ExitError with captured stderr) the
// child's stderr. Wraps with %w for errors.Is/As.
func wrapExecError(name string, args []string, dir string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		if trimmed := strings.TrimSpace(string(exitErr.Stderr)); trimmed != "" {
			return fmt.Errorf(
				"%s %s failed (in %s): %s: %w",
				name, strings.Join(args, " "), dir, trimmed, err,
			)
		}
	}

	return fmt.Errorf(
		"%s %s failed (in %s): %w",
		name, strings.Join(args, " "), dir, err,
	)
}

// gitEnvOverrides names the env vars Git consults to override the
// repository, work tree, index, and object database locations.
// Stripped from capture()'s child env so wrk isn't confused by hooks
// that set them.
var gitEnvOverrides = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_COMMON_DIR",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// sanitizedEnv returns the process env with git-dir overrides stripped
// and LANG/LC_ALL forced to C for stable stdout parsing.
func sanitizedEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+2)

	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		if isGitEnvOverride(key) {
			continue
		}
		out = append(out, kv)
	}

	// Later entries win in Go's exec.Cmd env handling, so append
	// LANG/LC_ALL last to override anything the parent set.
	out = append(out, "LANG=C", "LC_ALL=C")
	return out
}

func isGitEnvOverride(key string) bool {
	for _, name := range gitEnvOverrides {
		if key == name {
			return true
		}
	}
	return false
}
