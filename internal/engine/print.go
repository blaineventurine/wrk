package engine

import (
	"fmt"
	"io"

	"wrk/internal/planner"
)

// printPlan writes a human-readable execution plan.
func printPlan(
	w io.Writer,
	plan planner.Plan,
) error {
	if len(plan.Actions) == 0 &&
		len(plan.Conflicts) == 0 {
		if _, err := fmt.Fprintln(
			w,
			"No actions required.",
		); err != nil {
			return err
		}

		return nil
	}

	current := ""

	for _, planned := range plan.Actions {
		name := planned.Instance.Resource.Name

		if name != current {
			current = name

			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}

			if _, err := fmt.Fprintf(
				w,
				"[%s]\n",
				name,
			); err != nil {
				return err
			}
		}

		if _, err := fmt.Fprintf(
			w,
			"  • %s\n",
			formatAction(planned.Action),
		); err != nil {
			return err
		}
	}

	if len(plan.Conflicts) > 0 {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}

		if _, err := fmt.Fprintln(
			w,
			"Conflicts:",
		); err != nil {
			return err
		}

		for _, conflict := range plan.Conflicts {
			if _, err := fmt.Fprintf(
				w,
				"  • [%s] %s\n",
				conflict.Instance.Resource.Name,
				conflict.Message,
			); err != nil {
				return err
			}
		}
	}

	return nil
}

func formatAction(action planner.Action) string {
	switch action := action.(type) {

	case planner.CreateDirectory:
		return fmt.Sprintf(
			"Create directory: %s",
			action.Path,
		)

	case planner.Move:
		return fmt.Sprintf(
			"Move %s → %s",
			action.Source,
			action.Destination,
		)

	case planner.Remove:
		return fmt.Sprintf(
			"Remove %s",
			action.Path,
		)

	case planner.Symlink:
		return fmt.Sprintf(
			"Symlink %s → %s",
			action.Link,
			action.Target,
		)

	case planner.InitializeResource:
		count := len(action.Commands)

		suffix := ""
		if count != 1 {
			suffix = "s"
		}

		return fmt.Sprintf(
			"%s (%d command%s)",
			action.Description,
			count,
			suffix,
		)

	default:
		return fmt.Sprintf("%T", action)
	}
}
