package config

const (
	Filename      = ".wrk.yml"
	LocalFilename = ".wrk.local.yml"
)

// Config is the root wrk configuration.
type Config struct {
	Resources []Resource `yaml:"resources"`

	// Sources are the base filenames (relative to the repo root) from
	// which this config was loaded, in the order they were read. Populated
	// by Load; not read from YAML.
	Sources []string `yaml:"-"`
}
