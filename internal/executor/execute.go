package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"wrk/internal/commands"
	"wrk/internal/planner"
)

// Execute executes a plan.
//
// The plan is assumed to have already been validated.
func Execute(plan planner.Plan) error {
	for _, planned := range plan.Actions {
		switch action := planned.Action.(type) {

		case planner.CreateDirectory:
			if err := os.MkdirAll(
				action.Path,
				0o755,
			); err != nil {
				return err
			}

		case planner.Move:
			if _, err := os.Lstat(action.Destination); err == nil {
				return fmt.Errorf(
					"destination already exists: %s",
					action.Destination,
				)
			} else if !os.IsNotExist(err) {
				return err
			}

			if err := os.MkdirAll(
				filepath.Dir(action.Destination),
				0o755,
			); err != nil {
				return err
			}

			if err := move(
				action.Source,
				action.Destination,
			); err != nil {
				return err
			}

		case planner.Remove:
			if err := os.RemoveAll(
				action.Path,
			); err != nil {
				return err
			}

		case planner.Detach:
			if err := detach(
				action.Link,
				action.Target,
			); err != nil {
				return err
			}

		case planner.Symlink:
			if err := os.MkdirAll(
				filepath.Dir(action.Link),
				0o755,
			); err != nil {
				return err
			}

			// Ignore the error if the link doesn't exist.
			_ = os.Remove(action.Link)

			if err := os.Symlink(
				action.Target,
				action.Link,
			); err != nil {
				return err
			}

		case planner.InitializeResource:
			resolved, err := commands.Resolve(
				action.Commands,
				action.Context,
			)
			if err != nil {
				return err
			}

			for _, command := range resolved {
				if len(command.Args) == 0 {
					return fmt.Errorf("resolved command has no arguments")
				}

				cmd := exec.Command(
					command.Args[0],
					command.Args[1:]...,
				)

				cmd.Dir = command.Cwd

				cmd.Env = append(
					os.Environ(),
					environment(command.Env)...,
				)

				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Stdin = os.Stdin

				if err := cmd.Run(); err != nil {
					return err
				}
			}

		default:
			return fmt.Errorf(
				"unknown action type %T",
				action,
			)
		}
	}

	return nil
}

func environment(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	result := make([]string, 0, len(env))

	for key, value := range env {
		result = append(
			result,
			fmt.Sprintf("%s=%s", key, value),
		)
	}

	return result
}
