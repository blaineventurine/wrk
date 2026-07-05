package commands

import (
	"fmt"
	"path/filepath"

	"github.com/google/shlex"

	"wrk/internal/config"
	"wrk/internal/placeholders"
)

// Resolve expands placeholders in commands and prepares them for execution.
func Resolve(
	commands []config.Command,
	ctx placeholders.Context,
) ([]ResolvedCommand, error) {
	resolved := make(
		[]ResolvedCommand,
		0,
		len(commands),
	)

	for _, command := range commands {
		env := make(map[string]string, len(command.Env))

		for key, value := range command.Env {
			env[key] = placeholders.Expand(value, ctx)
		}

		cwd := ctx.Root
		if command.Cwd != "" {
			cwd = placeholders.Expand(command.Cwd, ctx)
		}

		run := placeholders.Expand(command.Run, ctx)

		args, err := shlex.Split(run)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid command %q: %w",
				command.Run,
				err,
			)
		}

		resolved = append(
			resolved,
			ResolvedCommand{
				Args: args,
				Cwd:  filepath.Clean(cwd),
				Env:  env,
			},
		)
	}

	return resolved, nil
}
