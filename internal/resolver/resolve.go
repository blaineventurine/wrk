package resolver

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
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
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ResourceInstance{}, err
	}
	absWs, err := filepath.Abs(workspacePath)
	if err != nil {
		return ResourceInstance{}, err
	}
	if absWs == absRoot {
		return ResourceInstance{}, fmt.Errorf(
			"resource %q resolves to the repository root; refusing",
			resource.Name,
		)
	}

	rel, err := filepath.Rel(absRoot, absWs)
	if err != nil {
		return ResourceInstance{}, err
	}
	if rel == "." || strings.HasPrefix(rel, "..") {
		return ResourceInstance{}, fmt.Errorf(
			"resource %q resolves outside the repository (%s)",
			resource.Name, workspacePath,
		)
	}

	instance := ResourceInstance{
		Resource:      resource,
		Root:          root,
		WorkspacePath: workspacePath,
		RelativePath:  filepath.ToSlash(rel),
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
