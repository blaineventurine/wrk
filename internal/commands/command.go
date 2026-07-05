package commands

// ResolvedCommand is ready for execution.
//
// All placeholders have been expanded, Cwd is always absolute, and
// Args contains the fully tokenized command.
type ResolvedCommand struct {
	Args []string
	Cwd  string
	Env  map[string]string
}
