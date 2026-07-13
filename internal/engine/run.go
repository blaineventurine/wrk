package engine

import (
	"fmt"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// RunPlan describes a `wrk run <resource>` invocation resolved
// against the current .wrk.yml. It is the read-only handoff between
// BuildRunPlan (preflight, plan assembly) and ExecuteRunPlan (actual
// hook execution). CLI callers use it to render a preview before
// prompting for confirmation.
type RunPlan struct {
	// Root is the workspace root, echoed by the plan preview.
	Root string

	// Resource is the configured resource being re-initialized. Its
	// Path is what users identify in the preview.
	Resource config.Resource

	// Commands is the initialize hook command list, hoisted here so
	// the plan preview can show a count without touching Resource.
	Commands []config.Command

	// Actions is the fully-built planner.Plan action list — one
	// InitializeResource{Force:true} entry per resolved instance,
	// same shape a bare Link plan would produce. ExecuteRunPlan
	// wraps this into a planner.Plan with WorkspaceRoot set and
	// dispatches to the executor.
	Actions []planner.PlannedAction
}

// Run re-executes a resource's initialize hook against the currently
// linked shared variant, atomically replacing the variant's contents.
// Use this to retry after fixing a hook or to refresh a variant without
// recomputing fingerprints.
//
// The workspace symlink is not touched — a successful Run leaves the
// same variant path in place with new bytes behind it.
//
// Errors surface when:
//   - repo is nil
//   - the resource is not configured
//   - the resource has no initialize hook
//   - the resource is detached in this workspace (swapping the shared
//     variant would have no visible effect on the workspace's
//     independent copy)
//   - the resolver, plan build, or hook execution fails
//
// Run is the plan-then-execute wrapper preserved for backward
// compatibility; it prints the assembled planner.Plan before executing.
// CLI callers that need to interpose confirmation should call
// BuildRunPlan + ExecuteRunPlan directly so the preview and prompt can
// slot between planning and mutation.
func Run(
	repo *repository.Repository,
	resourceName string,
	options Options,
) error {
	plan, err := BuildRunPlan(repo, resourceName, options)
	if err != nil {
		return err
	}
	full := planner.Plan{
		WorkspaceRoot: repo.Root,
		Actions:       plan.Actions,
	}
	if err := printPlan(options.Stdout, full); err != nil {
		return err
	}
	return executePlan(full, options)
}

// BuildRunPlan performs the read-only preflight for a `wrk run`
// invocation: config lookup, hook presence, detach guard, resolver
// expansion, and per-instance action assembly. It never mutates state.
//
// Errors returned from BuildRunPlan are user-facing refusals that no
// flag can override (a detached-resource run has no correct outcome
// against the shared variant, and an unknown resource is a typo).
func BuildRunPlan(
	repo *repository.Repository,
	resourceName string,
	options Options,
) (RunPlan, error) {
	if repo == nil {
		return RunPlan{}, fmt.Errorf("Run: nil repo")
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return RunPlan{}, Wrapf(ErrConfigInvalid,
			"check .wrk.yml for syntax errors or invalid resource paths",
			err, "%s", err.Error())
	}

	var target *config.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resourceName {
			target = &cfg.Resources[i]
			break
		}
	}
	if target == nil {
		return RunPlan{}, Newf(ErrResourceNotConfigured,
			"run 'wrk list' to see configured resources",
			"resource %q not configured", resourceName)
	}

	hookCommands, ok := target.Hooks["initialize"]
	if !ok || len(hookCommands) == 0 {
		return RunPlan{}, Newf(ErrResourceNoHook,
			"add an initialize hook to this resource in .wrk.yml",
			"resource %q has no initialize hook to run", resourceName)
	}

	// Refuse if this workspace has detached the resource: swapping the
	// shared variant would have no effect on the workspace's local copy,
	// so the user's mental model ("wrk run refreshed my resource") would
	// silently be wrong.
	reg, err := loadRegistry(repo)
	if err != nil {
		return RunPlan{}, err
	}

	// Same for isolation: the plan below targets the FINGERPRINT
	// variant path (location.For), but an isolated workspace's symlink
	// points at isolated-<hex>/ — the hook would refresh a variant
	// nobody looks at, a silent no-op from the user's perspective.
	iso, err := loadIsolation(repo)
	if err != nil {
		return RunPlan{}, err
	}

	instances, err := resolver.Resolve(repo.Root, *target)
	if err != nil {
		return RunPlan{}, err
	}
	if len(instances) == 0 {
		return RunPlan{}, fmt.Errorf(
			"resource %q resolved to no instances",
			resourceName,
		)
	}

	for _, instance := range instances {
		if isDetached(reg, repo.Root, instance.RelativePath) {
			return RunPlan{}, Newf(ErrResourceDetached,
				"run 'wrk relink' to reconnect this workspace, then retry",
				"resource %q is detached in this workspace; run `wrk relink` first",
				resourceName)
		}
		if _, isolated := isIsolated(iso, repo.Root, instance.RelativePath); isolated {
			return RunPlan{}, Newf(ErrResourceIsolated,
				"isolated resources hold per-workspace content wrk run cannot refresh; run `wrk relink` to return to fingerprint variants first",
				"resource %q is isolated in this workspace", resourceName)
		}
	}

	// Assemble one Force=true InitializeResource action per resolved
	// instance. WorkspaceRoot is set by ExecuteRunPlan when it wraps
	// these into a full planner.Plan so ensureContained gates on the
	// same guard the Link path uses.
	actions := make([]planner.PlannedAction, 0, len(instances))
	for _, instance := range instances {
		loc, err := location.For(
			options.StorageRoot,
			repo.RepositoryID,
			instance,
		)
		if err != nil {
			return RunPlan{}, err
		}

		actions = append(actions, planner.PlannedAction{
			Instance: instance,
			Action: planner.InitializeResource{
				Description: fmt.Sprintf(
					"re-run initialize hook for %s",
					target.Name,
				),
				Context:  instance.Context(loc.Path),
				Commands: hookCommands,
				Force:    true,
			},
		})
	}

	return RunPlan{
		Root:     repo.Root,
		Resource: *target,
		Commands: hookCommands,
		Actions:  actions,
	}, nil
}

// ExecuteRunPlan runs a pre-built RunPlan through the executor
// without printing the planner.Plan diagnostic block. CLI callers
// that already displayed their own preview + prompted for confirmation
// use this to avoid a double-print.
//
// On dry-run, the executor is bypassed and no output is produced —
// the CLI's caller is responsible for the preview.
func ExecuteRunPlan(
	repo *repository.Repository,
	plan RunPlan,
	options Options,
) error {
	full := planner.Plan{
		WorkspaceRoot: repo.Root,
		Actions:       plan.Actions,
	}
	return executePlan(full, options)
}
