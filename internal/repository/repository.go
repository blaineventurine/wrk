package repository

// Repository describes a repository managed by wrk.
type Repository struct {
	// Root is the repository root.
	Root string

	// RepositoryID is a stable identifier shared across all workspaces.
	RepositoryID string

	// metadataDir is the repository metadata directory (for example, the
	// common Git directory). It is private because only this package
	// needs to know where repository-local metadata lives.
	metadataDir string
	VCS         VCS
}

func newRepository(
	root string,
	repositoryID string,
	metadataDir string,
	vcs VCS,
) *Repository {
	return &Repository{
		Root:         root,
		RepositoryID: repositoryID,
		metadataDir:  metadataDir,
		VCS:          vcs,
	}
}
