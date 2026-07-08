package commands

import (
	"fmt"
	"path/filepath"

	"github.com/google/shlex"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
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

		if bad := unquotedShellOperator(args); bad != "" {
			return nil, fmt.Errorf(
				"invalid command %q: %q is a shell operator but hook `run:` is "+
					"tokenized, not shell-parsed; wrap the whole command as "+
					`sh -c "..." if you need shell semantics`,
				command.Run, bad,
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

// shellOperators are tokens that reach exec.Command as literal args
// only when a user forgot that `run:` is not shell-parsed.
var shellOperators = map[string]bool{
	"&&": true, "||": true, "|": true, "|&": true,
	";": true, "&": true,
	">": true, ">>": true, "<": true, "<<": true, "<<<": true,
	"2>": true, "2>>": true, "&>": true, "2>&1": true,
}

func unquotedShellOperator(args []string) string {
	for _, a := range args {
		if shellOperators[a] {
			return a
		}
	}
	return ""
}
