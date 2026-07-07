package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

var (
	statusAll      bool
	statusExitCode bool
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"st"},
	Short:   "Show the state of managed resources (read-only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		options := engine.Options{
			StorageRoot: storageRoot,
			Stdout:      os.Stdout,
		}

		var report *engine.StatusReport
		if statusAll {
			report, err = engine.StatusAll(repo, options)
		} else {
			report, err = engine.Status(repo, options)
		}
		if err != nil {
			return err
		}

		if err := printStatus(os.Stdout, report, statusAll); err != nil {
			return err
		}

		if statusExitCode && hasProblems(report.Rows) {
			return fmt.Errorf("one or more resources need attention")
		}

		return nil
	},
}

func printStatus(w *os.File, report *engine.StatusReport, all bool) error {
	// Configuration source header — only shown when a local override is
	// in play, so the default output stays clean.
	if len(report.Sources) > 1 {
		_, _ = fmt.Fprintln(w, dim("Config: "+strings.Join(report.Sources, " + ")))
		_, _ = fmt.Fprintln(w)
	}

	showOrigin := hasNonSharedOrigin(report.Rows)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer func() {
		_ = tw.Flush()
	}()

	if all {
		if showOrigin {
			_, _ = fmt.Fprintln(tw, "WORKSPACE\tRESOURCE\tPATH\tSTATE\tORIGIN\tFINGERPRINT")
		} else {
			_, _ = fmt.Fprintln(tw, "WORKSPACE\tRESOURCE\tPATH\tSTATE\tFINGERPRINT")
		}
	} else {
		if showOrigin {
			_, _ = fmt.Fprintln(tw, "RESOURCE\tPATH\tSTATE\tORIGIN\tFINGERPRINT")
		} else {
			_, _ = fmt.Fprintln(tw, "RESOURCE\tPATH\tSTATE\tFINGERPRINT")
		}
	}

	for _, r := range report.Rows {
		fields := []string{}
		if all {
			fields = append(fields, r.WorkspaceRoot)
		}
		fields = append(fields, r.Resource, r.Path, colorState(r.State))
		if showOrigin {
			fields = append(fields, string(r.Origin))
		}
		fields = append(fields, short(r.Fingerprint))

		_, _ = fmt.Fprintln(tw, strings.Join(fields, "\t"))
	}

	return nil
}

func hasNonSharedOrigin(rows []engine.ResourceStatus) bool {
	for _, r := range rows {
		if r.Origin != config.OriginShared {
			return true
		}
	}
	return false
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
