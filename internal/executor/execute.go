package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blaineventurine/wrk/internal/commands"
	"github.com/blaineventurine/wrk/internal/planner"
)

// Execute executes a plan.
//
// The plan is assumed to have already been validated.
func Execute(plan planner.Plan) error {
	for _, planned := range plan.Actions {
		switch action := planned.Action.(type) {

		case planner.CreateDirectory:
			if err := os.MkdirAll(
				action.Path,
				0o755,
			); err != nil {
				return err
			}

		case planner.Move:
			if err := ensureContained(plan, action.Source); err != nil {
				return err
			}

			if err := withLock(action.Destination, func() error {
				// Race: a peer may have provisioned the destination
				// while we waited for the lock. Verify shape (same
				// kind, no symlink) before discarding the workspace
				// copy — a broken symlink at dest would leave the user
				// with a link to garbage.
				destInfo, err := os.Lstat(action.Destination)
				if err == nil {
					srcInfo, srcErr := os.Lstat(action.Source)
					if srcErr != nil {
						return srcErr
					}

					// Idempotent recovery: if a prior run renamed but
					// was killed before removing source, contents
					// match; drop the redundant source without
					// invoking `wrk relink`.
					same, sameErr := sameContents(action.Source, action.Destination)
					if sameErr != nil {
						return fmt.Errorf(
							"comparing source %s and destination %s: %w",
							action.Source, action.Destination, sameErr,
						)
					}
					if same {
						return os.RemoveAll(action.Source)
					}

					if err := checkMoveDestinationKind(
						action.Destination, destInfo,
						srcInfo,
					); err != nil {
						return err
					}
					// Peer already provisioned. Same fingerprint =>
					// same contents; drop our source so Symlink can
					// take its place.
					return os.RemoveAll(action.Source)
				} else if !os.IsNotExist(err) {
					return err
				}

				if err := os.MkdirAll(
					filepath.Dir(action.Destination),
					0o755,
				); err != nil {
					return err
				}

				return move(action.Source, action.Destination)
			}); err != nil {
				return err
			}

		case planner.InitializeResource:
			shared := action.Context.Shared

			if err := withLock(shared, func() error {
				if !action.Force {
					// Double-check: skip the hook if the shared resource already exists
					// (a racing process ran the hook first).
					if _, err := os.Stat(shared); err == nil {
						return nil
					} else if !os.IsNotExist(err) {
						return err
					}
				}

				return runInitialize(action)
			}); err != nil {
				return err
			}
		case planner.Remove:
			if err := ensureContained(plan, action.Path); err != nil {
				return err
			}

			if err := safeRemove(action.Path); err != nil {
				return err
			}

		case planner.Detach:
			if err := ensureContained(plan, action.Link); err != nil {
				return err
			}

			if err := detach(
				action.Link,
				action.Target,
			); err != nil {
				return err
			}

		case planner.Symlink:
			if err := ensureContained(plan, action.Link); err != nil {
				return err
			}

			if err := os.MkdirAll(
				filepath.Dir(action.Link),
				0o755,
			); err != nil {
				return err
			}

			// Guard the Remove: on a real file/dir at Link, os.Remove
			// masks the truth with a bare "file exists" from the trailing
			// Symlink call. Refuse anything that isn't a symlink or
			// missing so the operator sees the real state; the plan
			// builder is supposed to catch this upstream, and if it
			// didn't, deleting a real file/dir here would be worse than
			// stopping.
			existing, err := os.Lstat(action.Link)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil {
				if existing.Mode()&os.ModeSymlink == 0 {
					return fmt.Errorf(
						"refusing to replace %s: expected a symlink or nothing, found %s",
						action.Link, fileKind(existing),
					)
				}
				if err := os.Remove(action.Link); err != nil {
					return fmt.Errorf(
						"removing existing symlink at %s: %w",
						action.Link, err,
					)
				}
			}

			if err := os.Symlink(
				action.Target,
				action.Link,
			); err != nil {
				return err
			}

		default:
			return fmt.Errorf(
				"unknown action type %T",
				action,
			)
		}
	}

	return nil
}

// ensureContained returns nil when path resolves under plan.WorkspaceRoot
// after following ancestor symlinks; otherwise it returns an error that
// refuses the mutation. Skipped when WorkspaceRoot is empty (older plans
// or unit tests that legitimately mutate outside a repository).
func ensureContained(plan planner.Plan, path string) error {
	if plan.WorkspaceRoot == "" {
		return nil
	}

	ok, err := containedIn(path, plan.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf(
			"checking containment of %s in %s: %w",
			path, plan.WorkspaceRoot, err,
		)
	}
	if !ok {
		return fmt.Errorf(
			"refusing to operate on %s: escapes workspace root %s (possible symlink)",
			path, plan.WorkspaceRoot,
		)
	}
	return nil
}

func environment(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	// Sort by key so that KEY=VALUE ordering is deterministic. Go maps
	// randomize iteration order; os/exec doesn't care about the order,
	// but reproducible hook logs are worth the cheap sort.
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(env))
	for _, key := range keys {
		result = append(
			result,
			fmt.Sprintf("%s=%s", key, env[key]),
		)
	}

	return result
}

// gitEnvOverrides lists environment variables that redirect git
// operations away from cwd-based discovery. wrk may itself be invoked
// from a git hook (post-checkout, post-merge), where git exports
// GIT_DIR pointing at the HOOK's repository — letting that leak into a
// user's initialize hook makes any git command inside the hook (git
// lfs pull, git submodule update) operate on the wrong repo.
//
// KEEP IN SYNC with internal/repository/backend.go's gitEnvOverrides —
// same list, same rationale, applied there to wrk's own VCS probes.
var gitEnvOverrides = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_COMMON_DIR":                   true,
	"GIT_INDEX_FILE":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
}

// hookEnv returns os.Environ() minus git-directory overrides. The
// hook's own env: map entries are appended by the caller AFTER this
// slice, and exec.Cmd lets later duplicates win, so explicit user
// configuration still overrides the stripping.
func hookEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if ok && gitEnvOverrides[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runInitialize provisions the shared resource atomically: the hook
// writes to <shared>.wrk-provisioning, and only on success does the
// scratch get renamed into place. A failed hook leaves nothing at the
// real path so the next Link re-runs cleanly.
//
// When `real` already exists (Force retry via `wrk run`), the old
// variant is renamed to <shared>.wrk-deleting for the atomic swap and
// then removed. A crash between the swap-aside and the second rename
// leaves the .wrk-deleting marker for `wrk gc`'s cleanBookkeepingDetect
// to sweep on the next run.
func runInitialize(action planner.InitializeResource) error {
	real := action.Context.Shared
	tmp := real + ".wrk-provisioning"

	// Clear stale scratch from a prior crash before substituting
	// {shared} → tmp.
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf(
			"clearing stale hook scratch %s: %w",
			tmp, err,
		)
	}

	// Override {shared} → tmp in the hook's context so all placeholder
	// expansions land in scratch. action.Context is left untouched.
	hookCtx := action.Context
	hookCtx.Shared = tmp

	resolved, err := commands.Resolve(action.Commands, hookCtx)
	if err != nil {
		return fmt.Errorf("resolving hook commands: %w", err)
	}

	for _, command := range resolved {
		if len(command.Args) == 0 {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("resolved hook command has no arguments")
		}

		cmd := exec.Command(command.Args[0], command.Args[1:]...)
		cmd.Dir = command.Cwd
		cmd.Env = append(hookEnv(), environment(command.Env)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Detach stdin: a hook that blocks reading it would wedge every
		// peer racing on the lock.
		cmd.Stdin = nil

		if err := cmd.Run(); err != nil {
			// Drop partial scratch so next Link re-runs. Force=true
			// keeps `real` intact — swap-aside hasn't happened yet.
			_ = os.RemoveAll(tmp)
			return &HookError{
				Command: strings.Join(command.Args, " "),
				Cwd:     command.Cwd,
				Err:     err,
			}
		}
	}

	// Nothing to commit if the hook didn't populate tmp. Force=true
	// keeps the pre-existing `real` intact — the hook produced no
	// replacement, so leaving the current variant alone is safer than
	// deleting it.
	if _, statErr := os.Lstat(tmp); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	}

	// Commit: for pre-existing `real` (Force path) rename it aside so
	// the atomic swap has a clean target. A crash between the two
	// renames leaves `<variant>.wrk-deleting` for `wrk gc`'s
	// cleanBookkeepingDetect to sweep.
	deleting := real + ".wrk-deleting"
	swappedAside := false
	if _, err := os.Lstat(real); err == nil {
		if err := os.RemoveAll(deleting); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf(
				"clearing stale swap-aside %s: %w",
				deleting, err,
			)
		}
		if err := os.Rename(real, deleting); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf(
				"swapping old variant %s aside: %w",
				real, err,
			)
		}
		swappedAside = true
	} else if !os.IsNotExist(err) {
		_ = os.RemoveAll(tmp)
		return err
	}

	if err := os.Rename(tmp, real); err != nil {
		_ = os.RemoveAll(tmp)
		if swappedAside {
			// Best-effort restore so the workspace symlink is not
			// left dangling. A failure here still leaves the
			// .wrk-deleting sibling for gc to sweep.
			_ = os.Rename(deleting, real)
		}
		return fmt.Errorf(
			"installing hook output from %s into %s: %w",
			tmp, real, err,
		)
	}

	// Old variant is safely aside; delete it. RemoveAll failures are
	// swept by `wrk gc`'s cleanBookkeepingDetect on the next run — the
	// atomic swap has already succeeded, so surfacing the error would
	// wrongly imply the retry failed.
	if swappedAside {
		_ = os.RemoveAll(deleting)
	}

	return nil
}

// safeRemove refuses to remove a repository root or infrastructure.
// Last-resort guard — validation should catch these upstream.
func safeRemove(path string) error {
	clean := filepath.Clean(path)

	if clean == "/" || clean == "." {
		return fmt.Errorf("refusing to remove %q", path)
	}

	// Refuse anything containing .git or .jj (a repository root).
	for _, marker := range []string{".git", ".jj"} {
		if _, err := os.Stat(filepath.Join(clean, marker)); err == nil {
			return fmt.Errorf(
				"refusing to remove %q: it contains repository metadata (%s)",
				path, marker,
			)
		}
	}

	return os.RemoveAll(clean)
}

// checkMoveDestinationKind verifies that a pre-existing destination is
// a plausible artifact for the winning racer to have provisioned: same
// kind as the workspace source (both directories or both regular
// files) and never a symlink. Anything else means we were about to
// discard the workspace copy in favor of garbage.
func checkMoveDestinationKind(destination string, destInfo, srcInfo os.FileInfo) error {
	destKind := fileKind(destInfo)
	srcKind := fileKind(srcInfo)

	// Symlinks are rejected outright: shared storage should be real
	// bytes, not indirection, even when the source happens to be a
	// symlink too.
	if destInfo.Mode()&os.ModeSymlink != 0 || destKind != srcKind {
		return fmt.Errorf(
			"shared destination %s exists but is not the expected kind (%s, want %s); refusing to discard workspace copy (run wrk relink or investigate manually)",
			destination, destKind, srcKind,
		)
	}
	return nil
}

// fileKind returns a short human word describing the file mode, used
// in refusal error messages so operators can see at a glance what the
// executor found on disk.
func fileKind(info os.FileInfo) string {
	switch mode := info.Mode(); {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "regular file"
	default:
		return "other"
	}
}

// HookError is returned by runInitialize when a user-configured
// initialize hook command exits non-zero. Callers use errors.As to
// distinguish hook failures from other executor errors and route
// accordingly — most notably the engine layer wraps HookError into
// engine.ErrHookCommandFailed so `wrk <cmd> --json` surfaces a
// stable code instead of the "unknown" fallback.
//
// Command is the resolved command line the executor tried to run
// (space-joined for a compact display); Cwd is the working directory
// the hook was configured with; Err is the underlying exec error the
// child process produced (typically *exec.ExitError with the exit
// status).
type HookError struct {
	Command string
	Cwd     string
	Err     error
}

// Error preserves the exact wording the previous fmt.Errorf produced
// so tests, log output, and human stderr messages that grep for
// "hook command failed" continue to work verbatim across the typed-
// error transition.
func (e *HookError) Error() string {
	return fmt.Sprintf(
		"hook command failed: %s (in %s): %v",
		e.Command, e.Cwd, e.Err,
	)
}

// Unwrap exposes the wrapped exec error so errors.Is / errors.As
// traverse into whatever the child process's exit produced (e.g. an
// *exec.ExitError).
func (e *HookError) Unwrap() error { return e.Err }
