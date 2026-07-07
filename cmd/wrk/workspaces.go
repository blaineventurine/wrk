package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"wrk/internal/engine"
)

var workspacesCmd = &cobra.Command{
	Use:   "workspaces",
	Short: "List all workspaces and their overall state",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		summaries, err := engine.WorkspaceSummaries(
			repo,
			engine.Options{
				StorageRoot: storageRoot,
				Stdout:      os.Stdout,
			},
		)
		if err != nil {
			return err
		}

		return printWorkspaces(os.Stdout, summaries)
	},
}

func printWorkspaces(w *os.File, summaries []engine.WorkspaceSummary) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()

	fmt.Fprintln(tw, "  WORKSPACE\tSTATE\tRESOURCES")

	for _, s := range summaries {
		marker := " "
		if s.IsCurrent {
			marker = "*"
		}

		fmt.Fprintf(
			tw, "%s %s\t%s\t%s\n",
			marker,
			s.Root,
			s.State,
			formatCounts(s.Counts),
		)
	}

	return nil
}

// formatCounts renders per-state counts as, e.g., "2 linked, 1 detached".
// States with zero counts are omitted. Order is stable for readability.
func formatCounts(counts map[engine.State]int) string {
	// Stable display order: healthy first, then actionable.
	order := []engine.State{
		engine.StateLinked,
		engine.StateExpected,
		engine.StateDetached,
		engine.StatePending,
		engine.StateMissing,
		engine.StateNotLinked,
		engine.StateStale,
		engine.StateConflict,
		engine.StateAbsent,
	}

	// Include any states not in the explicit order (future-proofing) at
	// the end, sorted alphabetically for determinism.
	seen := map[engine.State]bool{}
	for _, s := range order {
		seen[s] = true
	}
	var extras []engine.State
	for s := range counts {
		if !seen[s] {
			extras = append(extras, s)
		}
	}
	sort.Slice(extras, func(i, j int) bool { return extras[i] < extras[j] })
	order = append(order, extras...)

	var parts []string
	for _, s := range order {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func init() {
	rootCmd.AddCommand(workspacesCmd)
}
