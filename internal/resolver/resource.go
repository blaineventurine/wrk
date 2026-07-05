package resolver

import "wrk/internal/config"

// ResourceInstance is a concrete resource after glob expansion and
// placeholder resolution.
type ResourceInstance struct {
	Resource config.Resource

	Root string

	WorkspacePath string

	RelativePath string

	FingerprintInputs []string
}
