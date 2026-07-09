package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

var (
	statusAll      bool
	statusExitCode bool
	statusJSON     bool
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"st"},
	Short:   "Show the state of managed resources (read-only)",
	Long: "Read-only. Shows per-resource state for the current workspace " +
		"or, with --all, every workspace. Use --exit-code to have the " +
		"command exit non-zero (specifically: 1) when any resource is in " +
		"a state that `wrk link` would fix — handy for pre-commit hooks " +
		"and CI health checks. Real errors (missing repo, bad config) " +
		"still exit 2 and are distinguishable from the --exit-code signal.",
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

		if statusJSON {
			if err := printStatusJSON(os.Stdout, report, repo.Root); err != nil {
				return err
			}
		} else {
			if err := printStatus(os.Stdout, report, statusAll); err != nil {
				return err
			}
		}

		if statusExitCode && hasProblems(report.Rows) {
			// The status table above already tells the user what's wrong;
			// signalling via a sentinel keeps stderr silent while still
			// letting Execute distinguish this from a real error (exit 2).
			return exitCode{code: 1}
		}

		return nil
	},
}

func printStatus(w io.Writer, report *engine.StatusReport, all bool) error {
	// Configuration source header — only shown when a local override is
	// in play, so the default output stays clean.
	if len(report.Sources) > 1 {
		_, _ = fmt.Fprintln(w, dim("Config: "+strings.Join(report.Sources, " + ")))
		_, _ = fmt.Fprintln(w)
	}

	showOrigin := hasNonSharedOrigin(report.Rows)

	var headers []string
	if all {
		headers = append(headers, "WORKSPACE")
	}
	headers = append(headers, "RESOURCE", "PATH", "STATE")
	if showOrigin {
		headers = append(headers, "ORIGIN")
	}
	headers = append(headers, "FINGERPRINT")

	rows := []alignedRow{plainRow(headers)}
	for _, r := range report.Rows {
		var cells alignedRow
		if all {
			cells = append(cells, plainCell(r.WorkspaceRoot))
		}
		cells = append(cells,
			plainCell(r.Resource),
			plainCell(r.Path),
			coloredCell(colorState(r.State), string(r.State)),
		)
		if showOrigin {
			cells = append(cells, plainCell(string(r.Origin)))
		}
		cells = append(cells, plainCell(short(r.Fingerprint)))
		rows = append(rows, cells)
	}

	return writeAligned(w, rows)
}

// printStatusJSON writes report as versioned JSON followed by a
// trailing newline. This is the machine-readable equivalent of
// printStatus. primaryRoot is the workspace root of the current
// invocation — used to set IsPrimary on the emitted workspace groups
// so consumers can distinguish "the workspace wrk is running from"
// from other workspaces listed by --all.
func printStatusJSON(w io.Writer, report *engine.StatusReport, primaryRoot string) error {
	data, err := engine.MarshalStatusJSON(report, primaryRoot)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

func hasNonSharedOrigin(rows []engine.ResourceStatus) bool {
	for _, r := range rows {
		if r.Origin != config.OriginShared {
			return true
		}
	}
	return false
}

// hasProblems reports whether any row is in a state that a `wrk link`
// would fix. This covers actionable failures (conflict, stale, absent)
// as well as the "not yet run" states a fresh checkout is in
// (pending, missing, not-linked). Intentional resting states —
// detached, isolated, expected, linked — are not problems.
func hasProblems(rows []engine.ResourceStatus) bool {
	for _, r := range rows {
		switch r.State {
		case engine.StateConflict,
			engine.StateStale,
			engine.StateAbsent,
			engine.StatePending,
			engine.StateMissing,
			engine.StateNotLinked:
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
		"Exit 1 if any resource is in a state that 'wrk link' would fix "+
			"(real errors still exit 2)")

	statusCmd.Flags().BoolVar(&statusJSON, "json", false,
		"Emit machine-readable JSON instead of the tabular text output")
}
