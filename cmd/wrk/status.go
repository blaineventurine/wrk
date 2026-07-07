package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var (
	statusAll      bool
	statusExitCode bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of managed resources (read-only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		options := engine.Options{
			StorageRoot: storageRoot,
			Stdout:      os.Stdout,
		}

		var rows []engine.ResourceStatus
		if statusAll {
			rows, err = engine.StatusAll(repo, options)
		} else {
			rows, err = engine.Status(repo, options)
		}
		if err != nil {
			return err
		}

		if err := printStatus(os.Stdout, rows, statusAll); err != nil {
			return err
		}

		if statusExitCode && hasProblems(rows) {
			return fmt.Errorf("one or more resources need attention")
		}

		return nil
	},
}

func printStatus(w *os.File, rows []engine.ResourceStatus, all bool) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer func() {
		_ = tw.Flush()
	}()

	if all {
		_, _ = fmt.Fprintln(tw, "WORKSPACE\tRESOURCE\tPATH\tSTATE\tFINGERPRINT")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				r.WorkspaceRoot, r.Resource, r.Path, r.State, short(r.Fingerprint))
		}
		return nil
	}

	_, _ = fmt.Fprintln(tw, "RESOURCE\tPATH\tSTATE\tFINGERPRINT")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			r.Resource, r.Path, r.State, short(r.Fingerprint))
	}
	return nil
}

// hasProblems reports whether any row is in a state that would prevent a
// clean `wrk link`. Intentional states (detached, expected) are not
// problems.
func hasProblems(rows []engine.ResourceStatus) bool {
	for _, r := range rows {
		switch r.State {
		case engine.StateConflict,
			engine.StateStale,
			engine.StateAbsent:
			return true
		}
	}
	return false
}

func short(fp string) string {
	if fp == "" {
		return "-"
	}
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

func init() {
	rootCmd.AddCommand(statusCmd)

	statusCmd.Flags().BoolVar(&statusAll, "all", false,
		"Show status across all workspaces")

	statusCmd.Flags().BoolVar(&statusExitCode, "exit-code", false,
		"Exit non-zero if any resource is in a problem state")
}
