package engine

import (
	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
	"github.com/blaineventurine/wrk/internal/workspace"
)

// ResourceStatus describes the state of one resource instance in one
// workspace.
type ResourceStatus struct {
	WorkspaceRoot string
	Resource      string
	Path          string // workspace-relative path of the resource
	SharedPath    string
	Fingerprint   string // empty if the resource is not fingerprinted
	State         State
}

// State is the derived condition of a resource instance.
type State string

const (
	StateAbsent    State = "absent"     // nothing locally, no shared, no hook
	StateConflict  State = "conflict"   // local copy AND shared both exist
	StateDetached  State = "detached"   // independent copy from a prior detach
	StateExpected  State = "expected"   // create:false, provisioned out-of-band
	StateLinked    State = "linked"     // symlink -> correct shared path
	StateMissing   State = "missing"    // absent locally, shared exists (linkable)
	StateNotLinked State = "not-linked" // real copy present, not yet shared
	StatePending   State = "pending"    // nothing locally, no shared, hook available
	StateStale     State = "stale"      // symlink -> wrong target
)

// Status reports the state of every configured resource for the given
// repository. It never mutates anything.
func Status(repo *repository.Repository, options Options) ([]ResourceStatus, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, err
	}

	reg, err := loadRegistry(repo)
	if err != nil {
		return nil, err
	}

	var results []ResourceStatus
	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(repo.Root, resource)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			loc, err := location.For(options.StorageRoot, repo.RepositoryID, instance)
			if err != nil {
				return nil, err
			}
			state, err := workspace.Inspect(instance.WorkspacePath, loc.Path)
			if err != nil {
				return nil, err
			}

			derived := deriveState(instance, loc, state)
			// A conflict that we recorded as a deliberate detach is not a
			// problem — surface it distinctly.
			if derived == StateConflict &&
				isDetached(reg, repo.Root, instance.RelativePath) {
				derived = StateDetached
			}

			results = append(results, ResourceStatus{
				WorkspaceRoot: repo.Root,
				Resource:      instance.Resource.Name,
				Path:          instance.RelativePath,
				SharedPath:    loc.Path,
				Fingerprint:   loc.Fingerprint,
				State:         derived,
			})
		}
	}
	return results, nil
}

func deriveState(
	instance resolver.ResourceInstance,
	loc location.SharedLocation,
	state workspace.State,
) State {
	if state.WorkspaceSymlink {
		if state.WorkspaceLinkText == loc.Path {
			return StateLinked
		}
		return StateStale
	}

	if state.SharedExists {
		if state.WorkspaceExists {
			return StateConflict
		}
		return StateMissing
	}

	// Shared does not exist.
	if state.WorkspaceExists {
		return StateNotLinked
	}
	if len(instance.Resource.Hooks["initialize"]) > 0 {
		return StatePending
	}
	if !instance.Resource.ShouldCreate() {
		return StateExpected // provisioned out-of-band; expected
	}
	return StateAbsent
}

// StatusAll reports resource state across every live workspace of the
// repository.
func StatusAll(
	repo *repository.Repository,
	options Options,
) ([]ResourceStatus, error) {
	roots, err := repo.Workspaces()
	if err != nil {
		return nil, err
	}

	var results []ResourceStatus

	for _, root := range roots {
		wsRepo, err := repository.Detect(root, repo.VCS())
		if err != nil {
			return nil, err
		}

		rows, err := Status(wsRepo, options)
		if err != nil {
			return nil, err
		}

		results = append(results, rows...)
	}

	return results, nil
}
