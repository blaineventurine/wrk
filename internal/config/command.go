package config

// Command is a command defined in the configuration file.
//
// Run is shlex-tokenized (quoting honored) and exec'd directly, NOT
// interpreted by a shell. Shell operators (`|`, `&&`, `;`, `>`, ...)
// are rejected by Resolve to prevent confusing "sleep: invalid time
// interval: &&" errors. For shell semantics use an explicit shell:
//
//	run: sh -c "yarn install && yarn build"
//
// Placeholders ({root}, {parent}, {match}, {shared}) are expanded in
// Run, Cwd, and Env values before Run is tokenized. {shared} is a
// target path and may not exist when the hook runs; hooks writing
// there directly should `mkdir -p` first.
type Command struct {
	Run string            `yaml:"run"`
	Cwd string            `yaml:"cwd,omitempty"`
	Env map[string]string `yaml:"env,omitempty"`
}
