package config

const Filename = ".wrk.yml"

// Config is the root wrk configuration.
type Config struct {
	Resources []Resource `yaml:"resources"`
}
