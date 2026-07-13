package engine

import (
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

// storageRepoRoot returns the shared-storage subtree for repo —
// `<storage>/<repo-id>` — handed to resolver.ResolveWithStorage so glob
// resources also match relpaths that peer workspaces have already
// provisioned into storage (a fresh worktree has no on-disk glob
// matches of its own yet).
func storageRepoRoot(repo *repository.Repository, options Options) string {
	return filepath.Join(options.StorageRoot, repo.RepositoryID)
}

// resourcePlanner builds the plan for a single resolved resource instance.
type resourcePlanner func(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) planner.ResourcePlan

// buildPlan walks every configured resource, resolves it into concrete
// instances, and applies build to each one.
// buildPlan walks every configured resource, resolves it into concrete
// instances, and applies build to each one.
//
// prepare, if non-nil, runs once after the config is loaded and before any
// planning (used by link to update repository ignore rules).
func buildPlan(
	repo *repository.Repository,
	options Options,
	prepare func(cfg *config.Config) error,
	build resourcePlanner,
) (planner.Plan, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return planner.Plan{}, Wrapf(ErrConfigInvalid,
			"check .wrk.yml for syntax errors or invalid resource paths",
			err, "%s", err.Error())
	}
	printWarnings(cfg, options.Stdout)

	if prepare != nil {
		if err := prepare(cfg); err != nil {
			return planner.Plan{}, err
		}
	}

	iso, err := loadIsolation(repo)
	if err != nil {
		return planner.Plan{}, err
	}

	var plan planner.Plan
	for _, resource := range cfg.Resources {
		instances, err := resolver.ResolveWithStorage(repo.Root, storageRepoRoot(repo, options), resource)
		if err != nil {
			return planner.Plan{}, err
		}

		for _, instance := range instances {
			// Skip isolated resources: this workspace has explicitly
			// pinned a private per-workspace variant via
			// `wrk relink --isolate`. Link and detach must leave it
			// alone — repointing the symlink would silently undo the
			// user's isolation, and detaching would clobber the
			// isolated bytes with a workspace copy. (Relink is the
			// documented exit: it builds its own plan in
			// BuildRelinkPlan, which handles isolated resources via
			// IsolationExits instead of this skip.)
			if _, isolated := isIsolated(iso, repo.Root, instance.RelativePath); isolated {
				continue
			}
			loc, err := location.For(
				options.StorageRoot,
				repo.RepositoryID,
				instance,
			)
			if err != nil {
				return planner.Plan{}, err
			}

			state, err := workspace.Inspect(
				instance.WorkspacePath,
				loc.Path,
			)
			if err != nil {
				return planner.Plan{}, err
			}

			plan.AddResourcePlan(build(instance, loc, state))
		}
	}

	return plan, nil
}

// ignorePreparer builds a prepare hook that ensures wrk-managed paths (and
// the local config override file) are ignored by the VCS. Wildcard
// patterns for the executor's staging files (`.wrk-tmp`, `.wrk-backup`,
// `.wrk-lock`) are always included so a crash mid-operation cannot leave
// tracked-looking leftovers in `git status` — for directory resources
// those could be many MB of stray content.
func ignorePreparer(repo *repository.Repository) func(*config.Config) error {
	return func(cfg *config.Config) error {
		paths := []string{
			config.LocalFilename,
			"*.wrk-tmp",
			"*.wrk-backup",
			"*.wrk-lock",
		}
		for _, r := range cfg.Resources {
			paths = append(paths, r.Path)
		}
		return repo.Prepare(paths...)
	}
}
