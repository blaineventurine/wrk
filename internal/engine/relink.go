package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofrs/flock"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

// IsolationExit describes one isolated resource `wrk relink` will
// return to shared storage: the workspace symlink is repointed at the
// fingerprint variant (provisioning it if needed) and the private
// isolated variant is deleted. This is the documented exit from the
// `isolated` state — previously only possible by hand-editing
// `<metadata>/wrk/isolated.json`.
type IsolationExit struct {
	// ResourceName is the configured resource name, for display.
	ResourceName string
	// ResourcePath is the repository-relative resource path — the
	// isolation registry key within this workspace.
	ResourcePath string
	// StoragePath is the absolute path of the isolated variant that
	// will be deleted.
	StoragePath string
}

// RelinkPlan is the composed, read-only result of BuildRelinkPlan.
// Plan carries the ordinary reconnect actions; IsolationExits carries
// the isolated variants that will be discarded (destructive — their
// content is per-workspace and not reproducible by hooks).
type RelinkPlan struct {
	// Plan is the planner-level action list (Remove/Symlink/hook
	// provisioning), built as if the isolation exits below had already
	// happened.
	Plan planner.Plan

	// IsolationExits lists the isolated resources this relink returns
	// to shared storage. Executed BEFORE Plan so the filesystem
	// matches the state Plan was built against.
	IsolationExits []IsolationExit

	// SkippedIsolation lists isolation-registry resource paths for
	// this workspace that no longer correspond to any configured
	// resource. Relink leaves them untouched (their variants hold
	// non-reproducible content and no plan can be built for them);
	// they are surfaced so the user knows why the entry survives.
	SkippedIsolation []string
}

// HasWork reports whether executing the plan would mutate anything.
func (p RelinkPlan) HasWork() bool {
	return len(p.Plan.Actions) > 0 || len(p.IsolationExits) > 0
}

// BuildRelinkPlan builds a plan that reconnects the workspace to shared
// storage, discarding independent local copies AND (unlike `wrk link`)
// exiting any isolation pins for configured resources.
//
// For an isolated resource, the plan is built against the post-exit
// state: the workspace side is treated as absent (the isolated symlink
// is removed during the exit) and the shared side is probed while
// ignoring isolated/bookkeeping entries, so an un-fingerprinted
// resource whose storage directory exists only as the parent of the
// isolated variant is correctly re-provisioned rather than linked to
// an empty husk.
func BuildRelinkPlan(
	repo *repository.Repository,
	options Options,
) (RelinkPlan, error) {
	var relinkPlan RelinkPlan

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return RelinkPlan{}, Wrapf(ErrConfigInvalid,
			"check .wrk.yml for syntax errors or invalid resource paths",
			err, "%s", err.Error())
	}
	printWarnings(cfg, options.Stdout)

	if err := ignorePreparer(repo)(cfg); err != nil {
		return RelinkPlan{}, err
	}

	iso, err := loadIsolation(repo)
	if err != nil {
		return RelinkPlan{}, err
	}

	matched := map[string]bool{}

	var plan planner.Plan
	for _, resource := range cfg.Resources {
		instances, err := resolver.ResolveWithStorage(
			repo.Root, storageRepoRoot(repo, options), resource,
		)
		if err != nil {
			return RelinkPlan{}, err
		}

		for _, instance := range instances {
			loc, err := location.For(
				options.StorageRoot,
				repo.RepositoryID,
				instance,
			)
			if err != nil {
				return RelinkPlan{}, err
			}

			entry, isolated := isIsolated(iso, repo.Root, instance.RelativePath)
			if !isolated {
				state, err := workspace.Inspect(instance.WorkspacePath, loc.Path)
				if err != nil {
					return RelinkPlan{}, err
				}
				plan.AddResourcePlan(planner.BuildRelink(instance, loc, state))
				continue
			}

			matched[instance.RelativePath] = true
			relinkPlan.IsolationExits = append(relinkPlan.IsolationExits, IsolationExit{
				ResourceName: instance.Resource.Name,
				ResourcePath: instance.RelativePath,
				StoragePath:  entry.StoragePath,
			})

			// Post-exit state: workspace side gone (the exit removes
			// the isolated symlink), shared side probed while ignoring
			// isolated variants and bookkeeping scratch.
			state := workspace.State{
				SharedExists: sharedExistsBesidesIsolation(loc.Path),
			}
			plan.AddResourcePlan(planner.BuildRelink(instance, loc, state))
		}
	}

	for relPath := range iso[repo.Root] {
		if !matched[relPath] {
			relinkPlan.SkippedIsolation = append(relinkPlan.SkippedIsolation, relPath)
		}
	}
	sort.Strings(relinkPlan.SkippedIsolation)

	plan.WorkspaceRoot = repo.Root
	relinkPlan.Plan = plan
	return relinkPlan, nil
}

// Relink reconnects the current workspace to shared storage, discarding any
// independent local copies created by a previous `detach` and exiting any
// isolation pins.
//
// Relink is the plan-then-execute wrapper preserved for backward
// compatibility. It prints the plan before executing. CLI callers that
// need to interpose confirmation should call BuildRelinkPlan +
// ExecuteRelink directly instead — Relink would double-print.
func Relink(repo *repository.Repository, options Options) error {
	plan, err := BuildRelinkPlan(repo, options)
	if err != nil {
		return err
	}
	if err := PrintRelinkPlan(options.Stdout, plan); err != nil {
		return err
	}
	return ExecuteRelink(repo, plan, options)
}

// ExecuteRelink runs a pre-built relink plan without printing it.
//
// Order:
//
//  1. Conflicts abort before any mutation — including isolation exits,
//     so a conflicted plan never destroys an isolated variant.
//  2. Isolation exits run first: the plan's actions were built against
//     the post-exit filesystem state.
//  3. The planner actions execute (Remove/Symlink/provision).
//  4. The workspace's detach-registry entry is cleared so `wrk status`
//     no longer reports the resources as detached.
//
// Callers that print the plan themselves — e.g. the CLI's Build ->
// Print -> Confirm -> Execute flow — must use this instead of Relink
// to avoid a double-print.
func ExecuteRelink(repo *repository.Repository, plan RelinkPlan, options Options) error {
	if plan.Plan.HasConflicts() {
		return fmt.Errorf(
			"%d conflict(s) — see plan output above",
			len(plan.Plan.Conflicts),
		)
	}

	if options.DryRun {
		return nil
	}

	for _, exit := range plan.IsolationExits {
		if err := exitIsolation(repo, exit, options); err != nil {
			return fmt.Errorf(
				"exiting isolation for %s: %w", exit.ResourceName, err,
			)
		}
	}

	if err := executePlan(plan.Plan, options); err != nil {
		return err
	}

	if err := clearDetached(repo); err != nil {
		return fmt.Errorf("relink succeeded but failed to clear detach record: %w", err)
	}
	// Record this clone in the shared-storage clone registry so gc and
	// forget invoked from OTHER clones of the same repository see this
	// clone's pins. Best-effort bookkeeping — the relink itself is done.
	registerClone(repo, options)
	return nil
}

// exitIsolation removes one workspace's isolation pin: the isolated
// symlink is removed (only when it actually points into the isolated
// variant), the variant is deleted via the house rename-then-remove
// pattern, now-empty parent directories are tidied, and the registry
// entry is cleared. Idempotent: a rerun after a partial exit finishes
// the remaining steps.
//
// The variant delete runs under the variant's `.wrk-lock` (non-
// blocking): a held lock means another wrk process is touching this
// path and the exit refuses rather than yanking bytes out from under
// it.
func exitIsolation(repo *repository.Repository, exit IsolationExit, options Options) error {
	wsPath := filepath.Join(repo.Root, exit.ResourcePath)

	if info, err := os.Lstat(wsPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf(
				"%s is a real %s, not the expected symlink into the isolated variant; "+
					"resolve it manually (move it aside or `wrk detach`) and re-run",
				wsPath, humanFileKind(info),
			)
		}

		target, err := os.Readlink(wsPath)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(wsPath), target)
		}

		canonVariant, err := filepath.EvalSymlinks(exit.StoragePath)
		if err != nil {
			canonVariant = exit.StoragePath
		}
		resolvedTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			resolvedTarget = filepath.Clean(target)
		}

		// Remove only a link that points into the isolated variant.
		// Anything else (user re-pointed it) is left for the plan's
		// Symlink action, which replaces symlinks atomically.
		if isPathInside(canonVariant, resolvedTarget) {
			if err := os.Remove(wsPath); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	lockPath := exit.StoragePath + ".wrk-lock"
	lock := flock.New(lockPath)
	got, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !got {
		return fmt.Errorf(
			"isolated variant %s is locked by another process; retry when it finishes",
			exit.StoragePath,
		)
	}
	defer func() {
		_ = lock.Unlock()
	}()

	// Finish a crashed prior exit first, then rename-then-remove the
	// variant so a crash mid-delete leaves a `.wrk-deleting` marker
	// the next `wrk gc` sweeps.
	deleting := exit.StoragePath + ".wrk-deleting"
	if _, err := os.Lstat(deleting); err == nil {
		if err := executor.RemoveAllProgress(deleting, options.Progress); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(exit.StoragePath, deleting); err == nil {
		if err := executor.RemoveAllProgress(deleting, options.Progress); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Release and drop the lock file BEFORE tidying parents, so an
	// un-fingerprinted resource's now-empty storage parent is actually
	// empty and can be removed.
	_ = lock.Unlock()
	_ = os.Remove(lockPath)

	repoRoot := storageRepoRoot(repo, options)
	parent := filepath.Dir(exit.StoragePath)
	for parent != repoRoot &&
		strings.HasPrefix(parent, repoRoot+string(filepath.Separator)) {
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 0 {
			break
		}
		if err := os.Remove(parent); err != nil {
			break
		}
		parent = filepath.Dir(parent)
	}

	return clearIsolation(repo, repo.Root, exit.ResourcePath)
}

// sharedExistsBesidesIsolation reports whether sharedPath exists as a
// genuine shared copy once isolated variants and bookkeeping scratch
// are ignored.
//
// For fingerprinted resources sharedPath is the variant directory
// itself and isolated variants are siblings, so any content counts.
// For un-fingerprinted resources isolated variants nest INSIDE
// sharedPath — a directory whose only children are `isolated-*` (or
// bookkeeping scratch) exists only as the isolation parent, and
// linking a workspace to it would hand out an empty husk.
func sharedExistsBesidesIsolation(sharedPath string) bool {
	info, err := os.Stat(sharedPath)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	entries, err := os.ReadDir(sharedPath)
	if err != nil {
		// Unreadable: claim existence so the plan links rather than
		// re-running a hook against a directory we cannot inspect.
		return true
	}
	for _, entry := range entries {
		name := entry.Name()
		if isIsolatedVariantDir(name) || isBookkeeping(name) {
			continue
		}
		return true
	}
	return false
}

// humanFileKind mirrors the executor's fileKind wording for refusal
// messages without exporting it across the package boundary.
func humanFileKind(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "directory"
	case info.Mode().IsRegular():
		return "regular file"
	default:
		return "file"
	}
}
