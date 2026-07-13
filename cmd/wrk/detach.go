package main

import (
	"bytes"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// detachYes is bound to `--yes`/`-y`. Scripts and CI (which have no
// TTY to answer a prompt) advertise consent explicitly.
//
// detachForce is bound to `--force`. Detach has no per-command
// refusal reason today — BuildDetachPlan is a pure plan — so --force
// behaves as a stronger --yes for symmetry with the other destructive
// commands.
var (
	detachYes   bool
	detachForce bool
	detachJSON  bool
)

var detachCmd = &cobra.Command{
	Use:   "detach",
	Short: "Replace managed symlinks with independent workspace copies",
	Long: "Replace every managed symlink with an independent copy that " +
		"lives inside this workspace, so subsequent edits stay local. " +
		"Records which resources were detached so `wrk status` can " +
		"distinguish a deliberate detach from an accidental conflict. " +
		"Reverse safely with `wrk link` (keeps your local changes) or " +
		"destructively with `wrk relink` (discards them).\n\n" +
		"Destructive commands always print a plan first, then prompt " +
		"for confirmation on an interactive terminal. Non-interactive " +
		"callers must pass --yes. --dry-run prints the plan and exits " +
		"without touching anything.",
	RunE: func(cmd *cobra.Command, args []string) error {
		// H1: reject a hang-prone shape before touching anything —
		// see refuseJSONInteractive.
		if err := refuseJSONInteractive(detachJSON, dryRun, detachYes, detachForce); err != nil {
			return err
		}
		repo, err := currentRepository()
		if err != nil {
			if detachJSON {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return err
		}

		options := engine.Options{
			StorageRoot: storageRoot,
			DryRun:      dryRun,
			Stdout:      os.Stdout,
		}

		// Under --json every executor byte must be captured and
		// re-emitted inside the JSON envelope's `warnings` array so
		// the human-facing plain-text stream never mixes with the
		// machine-readable one. Progress becomes a running total the
		// result envelope carries as `bytesFreed`.
		var warningsBuf bytes.Buffer
		var bytesFreed int64
		if detachJSON {
			options.Stdout = &warningsBuf
			options.Progress = func(n int64) { bytesFreed += n }
		}

		plan, err := engine.BuildDetachPlan(repo, options)
		if err != nil {
			if detachJSON {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return err
		}

		if detachJSON {
			if err := runDetachJSON(plan, repo, options, &warningsBuf, &bytesFreed); err != nil {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return nil
		}

		if err := engine.PrintPlan(os.Stdout, plan); err != nil {
			return err
		}

		dec, err := Confirm(ConfirmOptions{
			Yes:    detachYes,
			Force:  detachForce,
			DryRun: dryRun,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		})
		if err != nil {
			return err
		}
		if dec != Proceed {
			return nil
		}

		return engine.ExecuteDetach(repo, plan, options)
	},
}

// runDetachJSON drives the --json path for `wrk detach`. The
// confirmation prompt still runs (with output suppressed) so a
// --force override still surfaces as a Proceed decision; a refusal
// / preview yields an envelope with a nil result field.
func runDetachJSON(
	plan planner.Plan,
	repo *repository.Repository,
	options engine.Options,
	warningsBuf *bytes.Buffer,
	bytesFreed *int64,
) error {
	attempted := false
	if !dryRun {
		dec, err := Confirm(ConfirmOptions{
			Yes:    detachYes,
			Force:  detachForce,
			DryRun: dryRun,
			Stdin:  os.Stdin,
			Stdout: io.Discard,
		})
		if err != nil {
			return err
		}
		if dec == Proceed {
			if err := engine.ExecuteDetach(repo, plan, options); err != nil {
				return err
			}
			attempted = true
		}
	}

	data, err := engine.MarshalDetachJSON(engine.DetachJSONInput{
		Plan:       plan,
		DryRun:     dryRun,
		Attempted:  attempted,
		BytesFreed: *bytesFreed,
		Warnings:   scanWarnings(warningsBuf),
	})
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, data)
}

func init() {
	rootCmd.AddCommand(detachCmd)

	detachCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Show planned actions without executing them",
	)
	detachCmd.Flags().BoolVarP(&detachYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	detachCmd.Flags().BoolVar(&detachForce, "force", false,
		"Override refusals and skip the prompt")
	detachCmd.Flags().BoolVar(&detachJSON, "json", false,
		"Emit machine-readable JSON (plan+result envelope) "+
			"instead of the human plan output")
}
