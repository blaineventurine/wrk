package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/repository"
)

// IsolatePlan describes the per-workspace variants that a subsequent
// ExecuteRelinkIsolate will materialize. Resources is the ordered,
// preflight-validated set — every entry is configured AND currently
// detached in this workspace. Preflight failures (unknown resource,
// linked resource, nothing to isolate) surface as errors from
// BuildRelinkIsolatePlan rather than fields on IsolatePlan: --force
// has no meaning for "isolate an undetached resource" (there is no
// safe fallback), so the CLI prompt path never sees them.
type IsolatePlan struct {
	// Root is the workspace root, echoed by the CLI's plan preview so
	// the user sees which worktree they are isolating.
	Root string

	// Resources is the ordered set of resources to isolate. Empty
	// slices from BuildRelinkIsolatePlan are always a hard error, so
	// ExecuteRelinkIsolate can treat an empty slice as "programmer
	// error" rather than "nothing to do".
	Resources []config.Resource
}

// RelinkIsolate promotes a workspace's detached resources into private
// per-workspace variants in shared storage. Peer workspaces of the same
// repository (sibling worktrees) are UNAFFECTED: only this workspace's
// symlink is repointed, only this workspace's detach + isolation records
// are edited.
//
// For each named resource, ExecuteRelinkIsolate:
//
//  1. Moves the workspace's detached copy into
//     `<storage>/<repositoryID>/<resource-path>/isolated-<hex>/`
//     (a fresh directory whose suffix is 16 hex chars — collision-free
//     across concurrent isolates against the same resource).
//  2. Installs a workspace-side symlink pointing at the isolated variant.
//  3. Records the pin in the isolation registry, keyed by
//     (workspaceRoot, resourcePath).
//  4. Drops the resource from the detach registry for THIS workspace
//     only — the on-disk state after step 2 is "linked to a private
//     variant", not "detached".
//
// If resourceNames is empty, every resource currently detached in this
// workspace is isolated. Empty names against a workspace with no
// detached resources is an error, not a no-op: the caller almost
// certainly meant to isolate something.
//
// Preflight (config load, name -> resource resolution, detached-in-this-
// workspace check) runs BEFORE any filesystem mutation so a bad name
// aborts cleanly without a partial state. The mutating loop runs under
// `withRegistryLock` so a concurrent detach or isolate on a sibling
// worktree cannot interleave its atomic rename with ours.
//
// Failure semantics:
//   - Symlink install fails after the executor Move: best-effort
//     `executor.Move` back to restore the workspace's files, then return
//     the wrap error. The workspace ends up in the state it started in
//     modulo an errant isolated directory that gc will sweep.
//   - Registry updates fail after the symlink is installed: the
//     filesystem is authoritative for the new state; log a diagnostic
//     and return the error. `wrk relink --isolate` is idempotent — the
//     next run reconciles the registry.
//   - A mid-loop failure across multiple resources leaves earlier
//     resources isolated and later ones untouched. Re-running the same
//     command completes the rest.
//
// RelinkIsolate is the plan-then-execute wrapper preserved for backward
// compatibility. CLI callers that need to interpose confirmation should
// call BuildRelinkIsolatePlan + ExecuteRelinkIsolate directly.
func RelinkIsolate(
	repo *repository.Repository,
	resourceNames []string,
	options Options,
) error {
	plan, err := BuildRelinkIsolatePlan(repo, resourceNames, options)
	if err != nil {
		return err
	}
	return ExecuteRelinkIsolate(repo, plan, options)
}

// BuildRelinkIsolatePlan runs the read-only preflight for a
// `wrk relink --isolate` invocation and returns the resolved,
// validated resource set. It never mutates the filesystem or any
// registry.
//
// See RelinkIsolate for the semantics of resourceNames (empty ->
// every currently-detached resource). Every returned error is a
// user-facing refusal that --force cannot override: isolating an
// undetached resource would silently steal bytes from peers, and an
// unknown resource name is almost always a typo.
func BuildRelinkIsolatePlan(
	repo *repository.Repository,
	resourceNames []string,
	options Options,
) (IsolatePlan, error) {
	if repo == nil {
		return IsolatePlan{}, fmt.Errorf("BuildRelinkIsolatePlan: nil repo")
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return IsolatePlan{}, err
	}

	detach, err := loadRegistry(repo)
	if err != nil {
		return IsolatePlan{}, err
	}
	detachedPaths := detach[repo.Root]

	// Empty names -> every currently-detached resource in this workspace.
	// Reverse-map registry paths to configured resource names so the
	// preflight loop below can validate them uniformly.
	if len(resourceNames) == 0 {
		for _, p := range detachedPaths {
			for i := range cfg.Resources {
				if cfg.Resources[i].Path == p {
					resourceNames = append(resourceNames, cfg.Resources[i].Name)
					break
				}
			}
		}
		if len(resourceNames) == 0 {
			return IsolatePlan{}, fmt.Errorf(
				"no detached resources to isolate in this workspace")
		}
	}

	// Preflight: every named resource MUST be configured AND currently
	// detached in this workspace. Only detached resources can be
	// isolated — isolating a linked resource would mean stealing bytes
	// that shared storage currently pins for every peer.
	resources := make([]config.Resource, 0, len(resourceNames))
	for _, name := range resourceNames {
		r := findResourceByName(cfg.Resources, name)
		if r == nil {
			return IsolatePlan{}, Newf(ErrResourceNotConfigured,
				"run 'wrk list' to see configured resources",
				"resource %q not configured", name)
		}
		detachedHere := false
		for _, p := range detachedPaths {
			if p == r.Path {
				detachedHere = true
				break
			}
		}
		if !detachedHere {
			return IsolatePlan{}, Newf(ErrResourceNotDetached,
				"run 'wrk detach' first, then retry --isolate",
				"resource %q is not detached in this workspace; "+
					"only detached resources can be isolated", name)
		}
		resources = append(resources, *r)
	}

	return IsolatePlan{Root: repo.Root, Resources: resources}, nil
}

// ExecuteRelinkIsolate applies a pre-built IsolatePlan. Callers that
// print the plan themselves — e.g. the CLI's Build -> Print -> Confirm
// -> Execute flow — use this instead of RelinkIsolate to skip the
// preflight already performed.
//
// On dry-run: prints one `# Would isolate ...` line per resource so
// interactive callers still see what would happen, matching the
// wrapper's historical output.
func ExecuteRelinkIsolate(
	repo *repository.Repository,
	plan IsolatePlan,
	options Options,
) error {
	if options.DryRun {
		for _, r := range plan.Resources {
			fmt.Fprintf(options.Stdout,
				"# Would isolate %s (%s) into private storage\n",
				r.Name, r.Path)
		}
		return nil
	}

	// Mutating phase: hold the registry flock across all resources so a
	// sibling process can't slip a detach/isolate/clear between our
	// per-resource updates. isolateOne calls loadIsolation/saveIsolation
	// and loadRegistry/saveRegistry DIRECTLY, not their `record*` wrappers
	// — those would reacquire the same flock and deadlock.
	return withRegistryLock(repo, func() error {
		for _, r := range plan.Resources {
			if err := isolateOne(repo, r, options); err != nil {
				return err
			}
		}
		return nil
	})
}

// isolateOne performs the four steps of a single isolate: Move, Symlink,
// isolation-registry write, detach-registry clear. Callers MUST already
// hold withRegistryLock — the registry helpers here are the lock-free
// primitives.
func isolateOne(
	repo *repository.Repository,
	r config.Resource,
	options Options,
) error {
	wsPath := filepath.Join(repo.Root, r.Path)

	suffix, err := randomHex(16)
	if err != nil {
		return fmt.Errorf("generating isolated variant suffix: %w", err)
	}
	isolatedPath := filepath.Join(
		options.StorageRoot,
		repo.RepositoryID,
		r.Path,
		"isolated-"+suffix,
	)
	if err := os.MkdirAll(filepath.Dir(isolatedPath), 0o755); err != nil {
		return fmt.Errorf("creating isolated parent dir: %w", err)
	}

	// Move the workspace's detached copy into isolated storage. Same-fs
	// is an atomic rename; cross-fs falls back to staged copy so a
	// partial copy never replaces the source.
	if err := executor.Move(wsPath, isolatedPath); err != nil {
		return fmt.Errorf(
			"moving detached copy to isolated storage: %w", err)
	}

	// Install the workspace symlink to the isolated variant. If this
	// fails, restore the workspace's files best-effort so the user isn't
	// left with an empty workspace path.
	if err := os.Symlink(isolatedPath, wsPath); err != nil {
		_ = executor.Move(isolatedPath, wsPath)
		return fmt.Errorf(
			"creating workspace symlink to isolated variant: %w", err)
	}

	// Filesystem is now in the isolated state. Any error after this
	// point is diagnostic-only: the on-disk truth is authoritative, and
	// re-running `wrk relink --isolate` reconciles the registry.

	isoReg, err := loadIsolation(repo)
	if err != nil {
		fmt.Fprintf(options.Stdout,
			"# warning: isolated %s at %s but failed to load isolation registry: %v\n",
			r.Name, isolatedPath, err)
		return fmt.Errorf(
			"reading isolation registry after symlink: %w", err)
	}
	if isoReg[repo.Root] == nil {
		isoReg[repo.Root] = map[string]isolationEntry{}
	}
	isoReg[repo.Root][r.Path] = isolationEntry{
		StoragePath: isolatedPath,
		CreatedAt:   nowUTC(),
	}
	if err := saveIsolation(repo, isoReg); err != nil {
		fmt.Fprintf(options.Stdout,
			"# warning: isolated %s at %s but failed to save isolation registry: %v\n",
			r.Name, isolatedPath, err)
		return fmt.Errorf("saving isolation registry: %w", err)
	}

	// Detach registry: the workspace no longer holds an independent
	// copy at r.Path, so drop the entry. Reload rather than reuse the
	// caller-supplied snapshot: a peer process may have amended it
	// between our load in RelinkIsolate and our acquisition of the
	// flock inside RelinkIsolate. Well, it couldn't since we hold the
	// flock across both — but reload anyway so this helper is safe to
	// call in isolation and matches how detach/relink do it.
	det, err := loadRegistry(repo)
	if err != nil {
		fmt.Fprintf(options.Stdout,
			"# warning: isolated %s at %s but failed to load detach registry: %v\n",
			r.Name, isolatedPath, err)
		return fmt.Errorf("reading detach registry after isolate: %w", err)
	}
	det[repo.Root] = removePath(det[repo.Root], r.Path)
	if len(det[repo.Root]) == 0 {
		delete(det, repo.Root)
	}
	if err := saveRegistry(repo, det); err != nil {
		fmt.Fprintf(options.Stdout,
			"# warning: isolated %s at %s but failed to save detach registry: %v\n",
			r.Name, isolatedPath, err)
		return fmt.Errorf("saving detach registry: %w", err)
	}

	return nil
}

// findResourceByName returns a pointer into `resources` for the entry
// whose Name equals `name`, or nil if none matches. Returning a pointer
// lets the caller take a copy without a second index scan.
func findResourceByName(resources []config.Resource, name string) *config.Resource {
	for i := range resources {
		if resources[i].Name == name {
			return &resources[i]
		}
	}
	return nil
}

// removePath returns paths with the first occurrence of target removed.
// Order is preserved. The returned slice may alias paths' backing array
// (in-place filter); callers that need to keep the original untouched
// MUST copy first.
func removePath(paths []string, target string) []string {
	out := paths[:0]
	for _, p := range paths {
		if p != target {
			out = append(out, p)
		}
	}
	return out
}

// nowUTC returns the current UTC time as an RFC3339 string. Behind a
// package-var so tests can freeze the timestamp for deterministic
// registry-content assertions without patching time.Now globally.
var nowUTC = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}
