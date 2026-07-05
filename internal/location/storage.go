package location

import (
	"path/filepath"

	"wrk/internal/fingerprint"
	"wrk/internal/resolver"
)

// For returns the shared storage location for a resource instance.
func For(
	storageRoot string,
	repositoryID string,
	instance resolver.ResourceInstance,
) (SharedLocation, error) {
	location := SharedLocation{
		Path: filepath.Join(
			storageRoot,
			repositoryID,
			instance.RelativePath,
		),
	}

	if len(instance.FingerprintInputs) == 0 {
		return location, nil
	}

	fp, err := fingerprint.Fingerprint(
		instance.Root,
		instance.FingerprintInputs...,
	)
	if err != nil {
		return SharedLocation{}, err
	}

	location.Fingerprint = fp
	location.Path = filepath.Join(
		location.Path,
		fp,
	)

	return location, nil
}
