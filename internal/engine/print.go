package engine

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/blaineventurine/wrk/internal/planner"
)

// writer wraps an io.Writer to accumulate the first write error.
type writer struct {
	w   io.Writer
	err error
}

func (x *writer) printf(format string, args ...any) {
	if x.err != nil {
		return
	}
	_, x.err = fmt.Fprintf(x.w, format, args...)
}

func (x *writer) println(args ...any) {
	if x.err != nil {
		return
	}
	_, x.err = fmt.Fprintln(x.w, args...)
}

func printPlan(w io.Writer, plan planner.Plan) error {
	out := &writer{w: w}

	// Group actions by resource name, preserving first-seen order.
	order := []string{}
	byResource := map[string][]planner.PlannedAction{}

	for _, pa := range plan.Actions {
		name := pa.Instance.Resource.Name
		if _, seen := byResource[name]; !seen {
			order = append(order, name)
		}
		byResource[name] = append(byResource[name], pa)
	}

	destructive := false

	for _, name := range order {
		out.printf("\n[%s]\n", name)
		for _, pa := range byResource[name] {
			desc, warn := describeAction(pa.Action)
			if warn {
				destructive = true
				out.printf("  ⚠ %s\n", desc)
			} else {
				out.printf("  • %s\n", desc)
			}
		}
	}

	if len(plan.Conflicts) > 0 {
		out.println("\nConflicts:")
		// stable ordering for readable output
		sort.SliceStable(plan.Conflicts, func(i, j int) bool {
			return plan.Conflicts[i].Instance.Resource.Name <
				plan.Conflicts[j].Instance.Resource.Name
		})
		for _, c := range plan.Conflicts {
			out.printf("  • [%s] %s\n", c.Instance.Resource.Name, c.Message)
		}
	}

	if destructive {
		out.println("\n⚠ This plan will permanently discard independent local copies.")
	}

	return out.err
}

// describeAction returns a human-readable description and whether the action
// is destructive (irreversibly removes user data).
func describeAction(action planner.Action) (string, bool) {
	switch a := action.(type) {
	case planner.CreateDirectory:
		return "create directory " + a.Path, false
	case planner.Move:
		return fmt.Sprintf("move %s → shared storage", a.Source), false
	case planner.Symlink:
		return fmt.Sprintf("link %s → %s", a.Link, a.Target), false
	case planner.Detach:
		return fmt.Sprintf("replace symlink %s with an independent copy", a.Link), false
	case planner.InitializeResource:
		return a.Description, false
	case planner.Remove:
		// A Remove of a symlink is harmless (relinking); a Remove of a real
		// file/dir discards independent local content.
		if isSymlink(a.Path) {
			return "remove stale symlink " + a.Path, false
		}
		return "discard independent local copy at " + a.Path, true
	default:
		return fmt.Sprintf("%T", action), false
	}
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}
