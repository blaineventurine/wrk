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
				// Double-check: a racing process may have already provisioned
				// the shared resource while we were waiting for the lock.
				// Before discarding the workspace copy, verify the
				// destination is a plausible provisioned artifact — same
				// kind as the source, and never a symlink (shared storage
				// stores real bytes, not indirection). A broken symlink or
				// wrong-kind file at the destination would otherwise cause
				// us to hand the user a link to garbage.
				destInfo, err := os.Lstat(action.Destination)
				if err == nil {
					srcInfo, srcErr := os.Lstat(action.Source)
					if srcErr != nil {
						return srcErr
					}

					// Idempotent-completion recovery: if a previous run
					// succeeded at Rename(tmp, dest) but was killed before
					// RemoveAll(source), source and destination now hold
					// byte-identical content. Detecting that lets us
					// silently complete the swap on the next Link instead
					// of forcing the operator through `wrk relink` — which
					// would discard any edits made to source between crash
					// and recovery. (There aren't any here: the contents
					// match, so we're safely removing a redundant copy.)
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
					// Winner already provisioned it. Our workspace copy is
					// now redundant; discard it so the trailing Symlink can
					// take its place. (Same fingerprint => equivalent
					// contents.)
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
				// Double-check: skip the hook if the shared resource already exists
				// (a racing process ran the hook first).
				if _, err := os.Stat(shared); err == nil {
					return nil
				} else if !os.IsNotExist(err) {
					return err
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

// runInitialize provisions the shared resource atomically by directing
// the initialize hook at a sibling scratch path and only committing that
// scratch into position after every hook command succeeds. A crashed or
// failed hook leaves nothing at the real shared path, so
// workspace.Inspect reports "not provisioned" and the next Link cleanly
// re-runs the hook from scratch. Without this, a hook that partially
// materializes `{shared}` and then fails would trick the outer
// double-check (`Stat(shared)` succeeds) into skipping the hook forever.
func runInitialize(action planner.InitializeResource) error {
	real := action.Context.Shared
	tmp := real + ".wrk-provisioning"

	// Clear any stale scratch from a previous crashed run before we
	// substitute {shared} → tmp. Leaving debris behind would let a hook
	// that assumes an empty `{shared}` see partial output from the
	// prior crash.
	if err := os.RemoveAll(tmp); err != nil {
		return fmt.Errorf(
			"clearing stale hook scratch %s: %w",
			tmp, err,
		)
	}

	// Copy the context and override Shared → tmp so every {shared}
	// placeholder in Run, Cwd, and Env resolves against the scratch
	// path. The action's own Context.Shared is left untouched for any
	// caller that inspects the plan.
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
		cmd.Env = append(os.Environ(), environment(command.Env)...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Hooks run while this process holds an advisory lock over the
		// shared resource; a hook that blocks on stdin would wedge every
		// peer racing on the same lock. Detach stdin explicitly so a hook
		// prompting for input fails fast (EOF) instead of hanging. Users
		// who need interactive setup should run the tool by hand outside
		// wrk.
		cmd.Stdin = nil

		if err := cmd.Run(); err != nil {
			// Drop the partial scratch so the next Link sees "not
			// provisioned" and re-runs the hook.
			_ = os.RemoveAll(tmp)
			return fmt.Errorf(
				"hook command failed: %s (in %s): %w",
				strings.Join(command.Args, " "),
				command.Cwd,
				err,
			)
		}
	}

	// Commit scratch → real atomically when the hook actually populated
	// scratch. A hook that ignored the `{shared}` placeholder (or wrote
	// somewhere else entirely) leaves tmp non-existent; that's not an
	// error we can distinguish from "hook intentionally didn't
	// provision", so leave `real` unmaterialized and let the next Link
	// re-run the hook. The invariant the atomic-provision protects is
	// "no partial output at real"; it does not require the hook to
	// produce anything.
	if _, statErr := os.Lstat(tmp); statErr == nil {
		if err := os.Rename(tmp, real); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf(
				"installing hook output from %s into %s: %w",
				tmp, real, err,
			)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	return nil
}

// safeRemove refuses to remove a path that appears to be a repository
// root or contain repository infrastructure. This is a last-resort
// guard; validation should catch these upstream.
func safeRemove(path string) error {
	clean := filepath.Clean(path)

	if clean == "/" || clean == "." {
		return fmt.Errorf("refusing to remove %q", path)
	}

	// A directory that contains .git or .jj is a repository root; never
	// remove it as part of a resource action.
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
