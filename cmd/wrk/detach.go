package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
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
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		options := engine.Options{
			StorageRoot: storageRoot,
			DryRun:      dryRun,
			Stdout:      os.Stdout,
		}

		plan, err := engine.BuildDetachPlan(repo, options)
		if err != nil {
			return err
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
}
