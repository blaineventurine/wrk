package resolver

import (
	"path/filepath"
	"strings"

	"wrk/internal/config"
	"wrk/internal/placeholders"
)

// Resolve expands a configured resource into one or more concrete resource
// instances.
func Resolve(
	root string,
	resource config.Resource,
) ([]ResourceInstance, error) {
	var workspacePaths []string

	if isGlob(resource.Path) {
		matches, err := filepath.Glob(
			filepath.Join(root, resource.Path),
		)
		if err != nil {
			return nil, err
		}

		workspacePaths = matches
	} else {
		workspacePaths = []string{
			filepath.Join(root, resource.Path),
		}
	}

	instances := make(
		[]ResourceInstance,
		0,
		len(workspacePaths),
	)

	for _, workspacePath := range workspacePaths {
		instance, err := newInstance(
			root,
			resource,
			workspacePath,
		)
		if err != nil {
			return nil, err
		}

		instances = append(
			instances,
			instance,
		)
	}

	return instances, nil
}

func newInstance(
	root string,
	resource config.Resource,
	workspacePath string,
) (ResourceInstance, error) {
	relative, err := filepath.Rel(
		root,
		workspacePath,
	)
	if err != nil {
		return ResourceInstance{}, err
	}

	instance := ResourceInstance{
		Resource:      resource,
		Root:          root,
		WorkspacePath: workspacePath,
		RelativePath:  filepath.ToSlash(relative),
	}

	// Fingerprint inputs are expanded with an empty shared path: a
	// fingerprint must never depend on the shared storage location.
	ctx := instance.Context("")

	fingerprintInputs := make(
		[]string,
		0,
		len(resource.Fingerprint),
	)

	for _, input := range resource.Fingerprint {
		fingerprintInputs = append(
			fingerprintInputs,
			placeholders.Expand(input, ctx),
		)
	}

	instance.FingerprintInputs = fingerprintInputs

	return instance, nil
}

func isGlob(path string) bool {
	return strings.ContainsAny(
		path,
		"*?[",
	)
}
