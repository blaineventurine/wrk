package resolver

import (
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
)

// ResourceInstance is a concrete resource after glob expansion and
// placeholder resolution.
type ResourceInstance struct {
	Resource config.Resource

	Root string

	WorkspacePath string

	RelativePath string

	FingerprintInputs []string
}

// Context builds the placeholder context for this instance.
//
// shared is the resolved shared storage path, or "" when it is not yet
// known (for example, when expanding fingerprint inputs, which must be
// independent of the shared location).
func (i ResourceInstance) Context(shared string) placeholders.Context {
	return placeholders.Context{
		Root:   i.Root,
		Parent: filepath.Dir(i.WorkspacePath),
		Match:  i.WorkspacePath,
		Shared: shared,
	}
}
