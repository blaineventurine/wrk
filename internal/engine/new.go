package engine

import "wrk/internal/repository"

// NewWorkspace creates and provisions a new workspace.
func NewWorkspace(
	repo *repository.Repository,
	destination string,
	options Options,
) error {
	// Ensure the primary workspace is initialized first.
	if err := Link(repo, options); err != nil {
		return err
	}

	newRepo, err := repo.CreateWorkspace(destination)
	if err != nil {
		return err
	}

	return Link(
		newRepo,
		options,
	)
}
