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
// On failure the error wraps the underlying error so callers can use
// errors.Is/errors.As (for example, to detect *exec.ExitError or
// exec.ErrNotFound). When the child produced stderr, the trimmed
// stderr is appended to the message for user-visible context.
//
// The child inherits a sanitized environment: git-directory overrides
// (GIT_DIR, GIT_WORK_TREE, ...) are stripped so wrk cannot be confused
// by hooks that set them, and LC_ALL=C forces a stable locale so
// output parsers don't have to deal with translated messages.
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

// passthrough runs a command in dir, wiring stdio to the process.
//
// stdin is wired to the parent's stdin so interactive prompts (git
// credential helpers, jj's occasional confirmations) work. stderr from
// the child is already visible on the user's terminal, so the wrapped
// error only records which command failed and where.
//
// Unlike capture, the child inherits the parent's full environment.
// passthrough drives user-facing commands (git worktree add, jj
// workspace add) that the user may reasonably expect to see their
// shell env — including git overrides — take effect.
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
// directory, and — when the underlying error is an *exec.ExitError with
// captured stderr — the process's stderr output. The underlying error
// is wrapped with %w so callers can errors.Is/errors.As through it
// (for example, to distinguish exec.ErrNotFound from a non-zero exit).
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

// gitEnvOverrides names the environment variables that Git consults to
// override the location of the repository, work tree, index, and object
// database. When wrk is invoked from a Git hook or an `env`-wrapped
// command, these can be set to point at some other repository — leaking
// them into `git remote get-url`, `git worktree list`, etc. would
// silently splice foreign state into wrk's decisions.
//
// The list is git's own headline set; more obscure overrides
// (GIT_CONFIG_*, GIT_NAMESPACE, ...) exist but do not affect the
// commands capture runs today.
var gitEnvOverrides = []string{
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_COMMON_DIR",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// sanitizedEnv returns the process environment with git-directory
// overrides stripped and LANG/LC_ALL forced to C, suitable for
// subprocesses whose stdout wrk parses.
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
