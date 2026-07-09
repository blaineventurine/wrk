package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var doctorJSONFlag bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Report the health of the current repository (read-only)",
	Long: `Composes config validation, ghost-workspace detection, and stale
bookkeeping checks into one summary. Read-only — nothing is mutated.

Exit codes are distinguishable so CI scripts can dispatch on them:
  0  everything looks healthy
  1  issues found (details in output — typically fixable with 'wrk gc')
  2  wrk itself couldn't run (bad flags, no repository, ...)
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}
		report, err := engine.Doctor(repo, engine.Options{
			StorageRoot: storageRoot,
			Stdout:      os.Stdout,
		})
		if err != nil {
			return err
		}
		if doctorJSONFlag {
			if err := printDoctorJSON(os.Stdout, report); err != nil {
				return err
			}
		} else {
			if err := printDoctor(os.Stdout, report); err != nil {
				return err
			}
		}
		if len(report.Issues) > 0 {
			// The summary above already tells the user what's wrong;
			// signalling via a sentinel keeps stderr silent while still
			// letting Execute distinguish this from a real error (exit 2).
			return exitCode{code: 1}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&doctorJSONFlag, "json", false,
		"Emit machine-readable JSON instead of the human-readable summary")
}

// printDoctorJSON writes the engine's machine-readable doctor JSON to
// w, followed by a trailing newline for shell-friendliness. It is the
// JSON equivalent of printDoctor.
func printDoctorJSON(w io.Writer, r *engine.DoctorReport) error {
	data, err := engine.MarshalDoctorJSON(r)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// printDoctor writes a human-readable health summary for the current
// repository: config validity, ghost workspaces, bookkeeping cruft,
// storage size, and a rollup line calling out the count of issues or
// declaring the repository healthy.
func printDoctor(w io.Writer, r *engine.DoctorReport) error {
	fmt.Fprintf(w, "Repository: %s (%s)\n", r.Root, r.VCS)
	fmt.Fprintf(w, "  Config:            %s\n", configStatus(r.Checks))
	fmt.Fprintf(w, "  Ghost workspaces:  %s\n", listOrNone(r.Checks.GhostWorkspaces))
	fmt.Fprintf(w, "  Bookkeeping cruft: %s\n", bookkeepingSummary(r.Checks))
	fmt.Fprintf(w, "  Storage size:      %s\n", engine.HumanSize(r.Checks.StorageSizeBytes))
	fmt.Fprintln(w)
	if len(r.Issues) == 0 {
		fmt.Fprintln(w, "Overall: healthy")
		return nil
	}
	fmt.Fprintf(w, "Overall: %d issue(s):\n", len(r.Issues))
	for _, issue := range r.Issues {
		fmt.Fprintf(w, "  - %s\n", issue)
	}
	return nil
}

// configStatus renders the ConfigValid/ConfigError pair as a single
// human-friendly line: "valid" or "invalid: <reason>".
func configStatus(c engine.DoctorChecks) string {
	if c.ConfigValid {
		return "valid"
	}
	return "invalid: " + c.ConfigError
}

// listOrNone renders a slice as "none" when empty, the sole element
// when it has exactly one entry, or "<n> entries" otherwise. Keeps
// the summary column narrow while still surfacing the single-item
// path directly (the common case for an isolated ghost).
func listOrNone(items []string) string {
	switch len(items) {
	case 0:
		return "none"
	case 1:
		return items[0]
	default:
		return fmt.Sprintf("%d entries", len(items))
	}
}

// bookkeepingSummary counts orphaned locks and stale provisioning /
// deleting / forgetting markers into a single number and hints at
// `wrk gc` for cleanup. Zero collapses to "none" so the healthy row
// stays quiet.
func bookkeepingSummary(c engine.DoctorChecks) string {
	n := len(c.OrphanedLocks) +
		len(c.StaleProvisioning) +
		len(c.StaleDeleting) +
		len(c.StaleForgetting)
	if n == 0 {
		return "none"
	}
	return fmt.Sprintf("%d item(s) — run `wrk gc`", n)
}
