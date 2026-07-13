package main

import (
	"bytes"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/progress"
	"github.com/blaineventurine/wrk/internal/repository"
)

// gcYes is bound to `--yes`/`-y`. Present so scripts and CI (which
// have no TTY to answer a prompt) can advertise consent explicitly.
//
// gcForce is bound to `--force`. `wrk gc` has no per-command refusal
// reasons in v1 — BuildGCPlan already excludes anything unsafe
// (pinned variants, held locks) — so --force behaves as a stronger
// synonym for --yes: it also skips the prompt, and it forwards the
// override intent to Confirm for symmetry with `wrk relink`.
var (
	gcYes      bool
	gcForce    bool
	gcJSON     bool
	gcExitCode bool
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Reclaim disk by pruning unused fingerprint variants and ghost workspaces",
	Long: "Reclaim disk and reconcile VCS metadata for this repository. " +
		"`wrk gc` runs three sweeps: ghost workspaces (git worktree " +
		"prune / jj workspace forget) whose working directory is gone " +
		"but VCS metadata still references them; fingerprint variant " +
		"subdirectories not currently symlinked from any live " +
		"workspace; and stale bookkeeping (orphaned .wrk-lock files, " +
		".wrk-provisioning scratch whose lock is free, .wrk-deleting " +
		"markers from a prior crash).\n\n" +
		"Destructive commands always print a plan first, then prompt " +
		"for confirmation on an interactive terminal. Non-interactive " +
		"callers must pass --yes. Concurrent `wrk link` operations are " +
		"respected: a variant whose lock is currently held is left " +
		"alone with a warning. --dry-run prints the plan and exits " +
		"without touching anything.",
	RunE: func(cmd *cobra.Command, args []string) error {
		// H1: reject a hang-prone shape before touching anything —
		// see refuseJSONInteractive.
		if err := refuseJSONInteractive(gcJSON, dryRun, gcYes, gcForce); err != nil {
			return err
		}
		repo, err := currentRepository()
		if err != nil {
			if gcJSON {
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
		if gcJSON {
			options.Stdout = &warningsBuf
			options.Progress = func(n int64) { bytesFreed += n }
		}

		plan, err := engine.BuildGCPlan(repo, options)
		if err != nil {
			if gcJSON {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return err
		}

		if gcJSON {
			if err := runGCJSON(plan, repo, options, &warningsBuf, &bytesFreed); err != nil {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return gcExitCodeSignal(plan)
		}

		engine.PrintGCPlan(os.Stdout, plan)

		// Nothing to sweep: skip the confirmation prompt entirely so
		// non-interactive callers (CI, pre-commit) can safely invoke
		// `wrk gc` as a probe without needing --yes.
		if plan.HasNothing() {
			// An empty plan is exit 0 even under --exit-code: the flag
			// signals "there was work to do", not "the command ran".
			return nil
		}

		dec, err := Confirm(ConfirmOptions{
			Yes:    gcYes,
			Force:  gcForce,
			DryRun: dryRun,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		})
		if err != nil {
			return err
		}
		if dec != Proceed {
			return gcExitCodeSignal(plan)
		}

		bar := progress.New(os.Stdout, plan.TotalBytesFreed, "Reclaiming")
		defer bar.Finish()
		options.Progress = bar.Add

		if err := engine.ExecuteGC(repo, plan, options); err != nil {
			return err
		}
		return gcExitCodeSignal(plan)
	},
}


// runGCJSON drives the --json path for `wrk gc`. The confirmation
// prompt still runs (with output suppressed) so a --force override
// still surfaces as a Proceed decision; a refusal / preview yields
// an envelope with a nil result field. An empty plan short-circuits
// to "not attempted", matching the human path's HasNothing() branch.
func runGCJSON(
	plan engine.GCPlan,
	repo *repository.Repository,
	options engine.Options,
	warningsBuf *bytes.Buffer,
	bytesFreed *int64,
) error {
	attempted := false
	if !dryRun && !plan.HasNothing() {
		dec, err := Confirm(ConfirmOptions{
			Yes:    gcYes,
			Force:  gcForce,
			DryRun: dryRun,
			Stdin:  os.Stdin,
			Stdout: io.Discard,
		})
		if err != nil {
			return err
		}
		if dec == Proceed {
			if err := engine.ExecuteGC(repo, plan, options); err != nil {
				return err
			}
			attempted = true
		}
	}

	data, err := engine.MarshalGCJSON(engine.GCJSONInput{
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
	rootCmd.AddCommand(gcCmd)
	gcCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show the plan without executing")
	gcCmd.Flags().BoolVarP(&gcYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	gcCmd.Flags().BoolVar(&gcForce, "force", false,
		"Override refusals and skip the prompt")
	gcCmd.Flags().BoolVar(&gcJSON, "json", false,
		"Emit machine-readable JSON (plan+result envelope) "+
			"instead of the human plan and progress output")
	gcCmd.Flags().BoolVar(&gcExitCode, "exit-code", false,
		"Exit 1 when the plan had cleanup to perform "+
			"(0 when nothing to do; real errors still exit 2)")
}

// gcExitCodeSignal maps a completed gc invocation to the caller's
// exit-code contract: silent exit 1 when --exit-code is set AND the
// plan actually had work in it, exit 0 otherwise. The signal fires
// regardless of whether the executor ran the work — the flag asks
// "was there cleanup to do?", not "did you sweep it?".
func gcExitCodeSignal(plan engine.GCPlan) error {
	if gcExitCode && !plan.HasNothing() {
		return exitCode{code: 1}
	}
	return nil
}
