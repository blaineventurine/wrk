package repository

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blaineventurine/wrk/internal/executor"
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

func (jjBackend) createWorkspace(root, dest, base string, stdout io.Writer) error {
	// "--" separates options from the (absolute) destination path so a
	// destination beginning with "-" cannot be reparsed as a flag.
	// resolveDestination already yields an absolute path, but the
	// separator is defensive: cheap, and pins the invariant in code.
	args := []string{"workspace", "add"}
	if base != "" {
		// --revision starts the new workspace's @ on top of <base>,
		// mirroring git's "new branch off <ref>" behaviour: the two
		// workspaces do not share a working-copy commit.
		args = append(args, "--revision", base)
	}
	args = append(args, "--", dest)
	return passthroughTo(stdout, root, "jj", args...)
}

func (jjBackend) workspaces(root string) ([]string, error) {
	entries, err := listJJWorkspaces(root)
	if err != nil {
		return nil, err
	}
	return resolveJJWorkspacePaths(root, entries)
}

// resolveJJWorkspacePaths converts jj's workspace listing into roots without
// silently dropping legacy workspaces created before jj 0.38, whose root is
// unset. The invoking root identifies one such entry when it is the only
// unresolved workspace. Any unresolved sibling is an error: omitting it could
// make callers such as gc delete resources that sibling still references.
func resolveJJWorkspacePaths(root string, entries []jjWorkspaceEntry) ([]string, error) {
	currentRoot := canonicalize(root)
	currentRecorded := false
	var unresolved []int

	for i := range entries {
		if entries[i].path == "" {
			unresolved = append(unresolved, i)
			continue
		}
		entries[i].path = canonicalize(entries[i].path)
		if entries[i].path == currentRoot {
			currentRecorded = true
		}
	}

	if !currentRecorded {
		if len(unresolved) != 1 {
			return nil, fmt.Errorf(
				"could not identify the current jj workspace root; %d workspaces have no recorded root",
				len(unresolved),
			)
		}
		entries[unresolved[0]].path = currentRoot
		unresolved = nil
	}

	if len(unresolved) > 0 {
		names := make([]string, 0, len(unresolved))
		for _, i := range unresolved {
			names = append(names, entries[i].name)
		}
		return nil, fmt.Errorf(
			"jj workspace(s) %s have no recorded root; run wrk from each legacy workspace or recreate it with the current jj version",
			strings.Join(names, ", "),
		)
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.path)
	}
	return paths, nil
}

// detectGhosts returns the roots of jj workspaces whose working-copy
// directory is missing. jj's own `self.root()` template evaluation
// emits an inline `<Error: ...>` string with the unresolvable path
// baked in — the same signal the CLI itself uses for missing dirs.
// We also stat the resolvable paths for belt-and-braces coverage,
// since a workspace whose working-copy dir is missing but that jj
// somehow still resolved (e.g. via a stale symlink) is still a
// ghost from wrk's perspective. Empty (non-nil) slice on clean.
func (jjBackend) detectGhosts(root string) ([]string, error) {
	entries, err := listJJWorkspaces(root)
	if err != nil {
		return nil, err
	}

	ghosts := make([]string, 0, len(entries))
	for _, e := range entries {
		if !workspaceIsGhost(e) {
			continue
		}
		if e.path == "" {
			// Template error we couldn't parse; skip rather than
			// return a bogus path the caller would try to display.
			continue
		}
		ghosts = append(ghosts, e.path)
	}

	return ghosts, nil
}

// pruneGhosts forgets each ghost workspace by name via
// `jj workspace forget`. jj happily forgets a workspace whose
// working copy is already gone, which is exactly the ghost case; the
// working-copy directory is only cleaned metadata-side. Returns the
// working-copy paths that were pruned so callers can surface them,
// or the empty (non-nil) slice when nothing was ghosted.
func (jjBackend) pruneGhosts(root string) ([]string, error) {
	entries, err := listJJWorkspaces(root)
	if err != nil {
		return nil, err
	}

	pruned := make([]string, 0, len(entries))
	for _, e := range entries {
		if !workspaceIsGhost(e) || e.name == "" || e.path == "" {
			continue
		}
		if _, err := capture(root, "jj", "workspace", "forget", "--", e.name); err != nil {
			return nil, err
		}
		pruned = append(pruned, e.path)
	}

	return pruned, nil
}

// removeWorkspace forgets the workspace whose working-copy root
// matches target via `jj workspace forget`, then removes the
// directory tree on disk. jj requires the workspace NAME, not its
// path, so we translate through the same template listing
// detectGhosts uses; canonicalization on both sides keeps macOS
// /private vs /var symlinks aligned. If no workspace resolves to
// target we return nil, matching the git backend's idempotent
// behavior. force is accepted for interface parity but ignored: as
// of jj 0.43 `workspace forget` has no --force flag and jj happily
// forgets a workspace whose working copy is dirty or gone.
//
// `jj workspace forget` is metadata-only — unlike `git worktree
// remove` it leaves the working-copy directory on disk. Match the
// git backend's user-visible contract by sweeping the directory
// after the forget succeeds. Order matters: if the sweep fails
// afterwards the user is left in the same "orphan directory" state
// the pre-fix jj backend produced, which `wrk gc` cannot catch (gc
// looks for the inverse: metadata present, directory missing), but
// a subsequent `rm -rf` by hand is safe.
//
// The sweep uses executor.RemoveAllProgress so the CLI-supplied
// onProgress callback fires for each regular file removed. Progress
// events on the multi-GB `node_modules`-style workspaces are the
// whole reason this backend does the sweep here instead of leaving
// it to `wrk gc`.
func (jjBackend) removeWorkspace(root, target string, _ bool, onProgress func(int64)) error {
	entries, err := listJJWorkspaces(root)
	if err != nil {
		return err
	}

	canonTarget := canonicalize(target)
	for _, e := range entries {
		if e.name == "" || e.path == "" {
			continue
		}
		if canonicalize(e.path) != canonTarget {
			continue
		}
		// "--" guards a workspace name that begins with "-".
		if err := passthrough(root, "jj", "workspace", "forget", "--", e.name); err != nil {
			return err
		}
		if err := executor.RemoveAllProgress(target, onProgress); err != nil {
			return fmt.Errorf(
				"jj workspace forget succeeded but failed to remove directory %s: %w",
				target, err,
			)
		}
		return nil
	}
	return nil
}

// uncommittedCount runs `jj diff --summary` in target and counts the
// non-empty lines. Each line is one file changed in the working-copy
// commit (@) versus its parent, which is jj's rough equivalent to
// git's uncommitted-changes signal.
//
// A probe failure — target missing a `.jj`, jj binary missing,
// permission denied — returns the underlying error. Callers may
// swallow it: a plan without an uncommitted-changes signal is still
// useful, and the executor sees the same failure at commit time.
func (jjBackend) uncommittedCount(target string) (int, error) {
	out, err := capture(target, "jj", "diff", "--summary")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// jjWorkspaceEntry pairs a workspace name with the path jj reported
// for its root. `ghost` is true when jj's template evaluation itself
// failed — i.e. the working copy is unreachable and the path was
// recovered from jj's inline `<Error: ...>` message. When ghost is
// false the caller may still stat the path to catch races.
type jjWorkspaceEntry struct {
	name  string
	path  string
	ghost bool
}

// listJJWorkspaces enumerates the repo's workspaces with a template
// that pairs each name with its root, tolerating jj's inline error
// syntax so ghosts can be reported without a second jj call per name.
func listJJWorkspaces(root string) ([]jjWorkspaceEntry, error) {
	out, err := capture(
		root,
		"jj", "workspace", "list",
		"--template", `self.name() ++ "\t" ++ self.root() ++ "\n"`,
	)
	if err != nil {
		return nil, err
	}

	var entries []jjWorkspaceEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		name, rootField, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		rootField = strings.TrimSpace(rootField)

		e := jjWorkspaceEntry{name: name}
		if path, isErr := parseJJInlineError(name, rootField); isErr {
			e.path = path
			e.ghost = true
		} else {
			e.path = rootField
		}
		if e.path == "" {
			resolved, resolveErr := capture(
				root, "jj", "workspace", "root", "--name", name,
			)
			unresolvableRoot := ""
			if resolveErr != nil {
				unresolvableRoot = parseJJUnresolvableRoot(resolveErr.Error())
			}
			switch {
			case resolveErr == nil:
				e.path = strings.TrimSpace(resolved)
			case strings.Contains(resolveErr.Error(), "Workspace has no recorded path:"):
				// Legacy workspace created before jj 0.38. Leave the
				// path empty so resolveJJWorkspacePaths can infer the
				// invoking workspace or fail closed for a sibling.
			case unresolvableRoot != "":
				e.path = unresolvableRoot
				e.ghost = true
			default:
				return nil, resolveErr
			}
		}
		entries = append(entries, e)
	}

	return entries, nil
}

// parseJJUnresolvableRoot extracts the workspace path from jj 0.44's
// `workspace root --name` error for a recorded workspace whose directory is
// gone. jj 0.43 exposed this path through the list template instead.
func parseJJUnresolvableRoot(s string) string {
	const prefix = "Cannot resolve absolute workspace path: "
	start := strings.Index(s, prefix)
	if start < 0 {
		return ""
	}
	path := s[start+len(prefix):]
	if end := strings.IndexByte(path, '\n'); end >= 0 {
		path = path[:end]
	}
	return filepath.Clean(strings.TrimSpace(path))
}

// workspaceIsGhost is true either when jj's template evaluation
// surfaced the inline error (definitely a ghost) or when the
// reported path fails to stat (race or symlink oddity).
func workspaceIsGhost(e jjWorkspaceEntry) bool {
	if e.ghost {
		return true
	}
	if e.path == "" {
		return false
	}
	_, err := os.Stat(e.path)
	return err != nil
}

// parseJJInlineError recovers the unresolvable workspace path from
// jj's template-time error string. jj 0.43 emits it in the shape:
//
//	<Error: Failed to resolve workspace root: <NAME>: <PATH>: <OSERR>>
//
// where <PATH> is a jj-internal form like
// `.../<repo>/.jj/repo/../../../<name>` that filepath.Clean
// normalizes to the actual working-copy root. Returns (path, true)
// only when the shape matches; anything else yields ("", false) so
// callers know the entry is not a template error at all.
func parseJJInlineError(name, s string) (string, bool) {
	const errPrefix = "<Error:"
	const errSuffix = ">"
	if !strings.HasPrefix(s, errPrefix) || !strings.HasSuffix(s, errSuffix) {
		return "", false
	}

	// Strip the <Error: … > envelope, then the fixed jj error prefix.
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, errPrefix), errSuffix))
	const rootPrefix = "Failed to resolve workspace root: "
	if !strings.HasPrefix(inner, rootPrefix) {
		return "", true
	}
	inner = strings.TrimPrefix(inner, rootPrefix)

	// The remainder is "<NAME>: <PATH>: <OS ERROR>". The workspace
	// name is under our control (comes from the tab-split above) so
	// we can strip it exactly; the OS error suffix has no ": " in
	// its own message, so the LAST ": " separates the path from it.
	inner = strings.TrimPrefix(inner, name+": ")
	idx := strings.LastIndex(inner, ": ")
	if idx < 0 {
		return "", true
	}
	return filepath.Clean(inner[:idx]), true
}
