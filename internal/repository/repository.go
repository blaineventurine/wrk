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

	// backend implements VCS-specific operations and is the single source
	// of truth for which VCS this repository uses.
	backend backend
}

func newRepository(
	root string,
	repositoryID string,
	metadataDir string,
	backend backend,
) *Repository {
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
