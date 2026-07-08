package repository

// Repository describes a repository managed by wrk.
type Repository struct {
	// Root is the repository root.
	Root string

	// RepositoryID is a stable identifier shared across all workspaces.
	RepositoryID string

	// metadataDir is the repository metadata directory (for example, the
	// common Git directory). Private: only this package needs it.
	metadataDir string

	// backend implements VCS-specific operations and is the single
	// source of truth for which VCS this repository uses.
	backend backend
}

func newRepository(
	root string,
	repositoryID string,
	metadataDir string,
	backend backend,
) *Repository {
	// Internal constructor: every caller lives in this package and has
	// already resolved these values. A nil backend or empty root is a
	// programmer bug that would surface much later as a confusing nil
	// deref inside the VCS layer, so refuse it up front.
	if root == "" || backend == nil {
		panic("repository: newRepository called with empty root or nil backend")
	}
	return &Repository{
		Root:         root,
		RepositoryID: repositoryID,
		metadataDir:  metadataDir,
		backend:      backend,
	}
}

// VCS returns the detected version-control system.
func (r *Repository) VCS() VCS {
	return r.backend.kind()
}

// MetadataDir returns the repository's shared metadata directory (for
// example, the common Git directory). Used by wrk to store repository-
// local state such as the detach registry.
func (r *Repository) MetadataDir() string {
	return r.metadataDir
}
