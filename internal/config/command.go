package config

// Command is a command defined in the configuration file.
//
// Run is tokenized with shell-like word splitting (quoting and escapes are
// honored) and executed directly — it is NOT interpreted by a shell. This
// means shell-only constructs do not work:
//
//   - Pipelines and operators: "a | b", "a && b", "a; b"
//   - Redirections: "cmd > file"
//   - Variable expansion: "echo $HOME"
//   - Inline environment assignments: "FOO=bar cmd"
//
// To set environment variables, use the Env map instead of an inline
// assignment. To use shell features, invoke a shell explicitly, e.g.:
//
//	run: sh -c "yarn install && yarn build"
//
// Placeholders ({root}, {parent}, {match}, {shared}) are expanded in Run,
// Cwd, and Env values before Run is tokenized.
type Command struct {
	Run string            `yaml:"run"`
	Cwd string            `yaml:"cwd,omitempty"`
	Env map[string]string `yaml:"env,omitempty"`
}
