package config

// Command is a command defined in the configuration file.
type Command struct {
	Run string            `yaml:"run"`
	Cwd string            `yaml:"cwd,omitempty"`
	Env map[string]string `yaml:"env,omitempty"`
}
