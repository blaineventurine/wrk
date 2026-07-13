package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var workspacesCmd = &cobra.Command{
	Use:     "workspaces",
	Aliases: []string{"ws"},
	Short:   "List all workspaces and their overall state",
	Long: "Read-only. Shows every live worktree (git) or workspace (jj) " +
		"for this repository along with its rolled-up resource state — " +
		"linked, detached, partial, pending, or unhealthy. A `*` marks " +
		"the current workspace. Use `wrk status --all` for the per-" +
		"resource breakdown.",
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

		if workspacesJSON {
			return printWorkspacesJSON(os.Stdout, summaries)
		}

		return printWorkspaces(os.Stdout, summaries)
	},
}

func printWorkspaces(w io.Writer, summaries []engine.WorkspaceSummary) error {
	rows := []alignedRow{plainRow([]string{"  WORKSPACE", "STATE", "RESOURCES"})}

	for _, s := range summaries {
		marker := " "
		if s.IsCurrent {
			marker = "*"
		}
		rows = append(rows, alignedRow{
			plainCell(marker + " " + s.Root),
			coloredCell(colorWorkspaceState(s.State), string(s.State)),
			plainCell(formatCounts(s.Counts)),
		})
	}

	return writeAligned(w, rows)
}

// formatCounts renders per-state counts as, e.g., "2 linked, 1 detached".
// States with zero counts are omitted. Order is stable for readability.
func formatCounts(counts map[engine.State]int) string {
	// Stable display order: healthy first, then actionable.
	order := []engine.State{
		engine.StateLinked,
		engine.StateExpected,
		engine.StateDetached,
		engine.StateIsolated,
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

// workspacesJSON is bound to `--json`. When set, workspaces emits
// a single JSON envelope (schema/kind + list of workspace summaries)
// instead of the tabular text output.
var workspacesJSON bool

// printWorkspacesJSON writes the engine's machine-readable workspaces
// JSON to w, followed by a trailing newline for shell-friendliness.
// It is the JSON equivalent of printWorkspaces and never falls
// through to the tabular path.
func printWorkspacesJSON(w io.Writer, summaries []engine.WorkspaceSummary) error {
	data, err := engine.MarshalWorkspacesJSON(summaries)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

func init() {
	rootCmd.AddCommand(workspacesCmd)
	workspacesCmd.Flags().BoolVar(&workspacesJSON, "json", false,
		"Emit machine-readable JSON instead of the tabular text output")
}
