package config

// Resource describes a managed resource.
type Resource struct {
	Name        string               `yaml:"name"`
	Path        string               `yaml:"path"`
	Fingerprint []string             `yaml:"fingerprint,omitempty"`
	Hooks       map[string][]Command `yaml:"hooks,omitempty"`
	Create      *bool                `yaml:"create,omitempty"`
	// Origin and sourceIndex are populated by Load, not from YAML.
	// sourceIndex is the 0-based per-file position.
	Origin      Origin `yaml:"-"`
	sourceIndex int
}

// ShouldCreate reports whether the resource should be created if it does
// not already exist.
func (r Resource) ShouldCreate() bool {
	if r.Create == nil {
		return true
	}

	return *r.Create
}
