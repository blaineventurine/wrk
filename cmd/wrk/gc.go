package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
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
	gcYes   bool
	gcForce bool
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
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		options := engine.Options{
			StorageRoot: storageRoot,
			DryRun:      dryRun,
			Stdout:      os.Stdout,
		}

		plan, err := engine.BuildGCPlan(repo, options)
		if err != nil {
			return err
		}

		engine.PrintGCPlan(os.Stdout, plan)

		// Nothing to sweep: skip the confirmation prompt entirely so
		// non-interactive callers (CI, pre-commit) can safely invoke
		// `wrk gc` as a probe without needing --yes.
		if plan.HasNothing() {
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
			return nil
		}

		return engine.ExecuteGC(repo, plan, options)
	},
}

func init() {
	rootCmd.AddCommand(gcCmd)
	gcCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show the plan without executing")
	gcCmd.Flags().BoolVarP(&gcYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	gcCmd.Flags().BoolVar(&gcForce, "force", false,
		"Override refusals and skip the prompt")
}
