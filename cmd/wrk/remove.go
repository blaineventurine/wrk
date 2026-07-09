package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

// removeYes is bound to `--yes`/`-y`. Scripts and CI (which have no
// TTY to answer a prompt) advertise consent explicitly.
//
// removeForce is bound to `--force`. It behaves as a stronger --yes:
// it skips the prompt AND overrides the soft refusal reasons that
// BuildRemovePlan populates (uncommitted VCS changes, detached-file
// registry entries). The primary-workspace and current-workspace
// guards are hard errors on the plan builder, so --force cannot
// reach them.
var (
	removeYes   bool
	removeForce bool
)

var removeCmd = &cobra.Command{
	Use:   "remove <workspace>",
	Short: "Tear down a workspace (VCS worktree + detach registry entry)",
	Long: "Remove the workspace at <workspace>. `<workspace>` follows " +
		"the same sibling-default policy as `wrk new`: a bare name " +
		"resolves to a sibling of the primary workspace, a relative " +
		"path joins against the primary, an absolute path is used as " +
		"given.\n\n" +
		"The command prints a plan first, then prompts for confirmation " +
		"on an interactive terminal. Non-interactive callers must pass " +
		"--yes. Refusal reasons — uncommitted VCS changes, detached-file " +
		"registry entries — require --force to override. The primary " +
		"workspace and the workspace the caller is currently inside are " +
		"hard errors that no flag can bypass. --dry-run prints the plan " +
		"and exits without touching anything.",
	Args: cobra.ExactArgs(1),
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

		plan, err := engine.BuildRemovePlan(repo, args[0], options)
		if err != nil {
			return err
		}

		printRemovePlan(os.Stdout, plan)

		dec, err := Confirm(ConfirmOptions{
			Yes:     removeYes,
			Force:   removeForce,
			DryRun:  dryRun,
			Refusal: plan.Refusal,
			Stdin:   os.Stdin,
			Stdout:  os.Stdout,
		})
		if err != nil {
			return err
		}
		if dec != Proceed {
			return nil
		}

		return engine.ExecuteRemove(repo, plan, removeForce)
	},
}

// printRemovePlan renders the RemovePlan for the user. Kept small
// and inline here — there is no dedicated engine.PrintRemovePlan
// because the plan has a fixed, terse shape (one target, a handful
// of scalar fields) that a helper would only obscure.
func printRemovePlan(w *os.File, plan engine.RemovePlan) {
	fmt.Fprintf(w, "Removing workspace: %s\n\n", plan.Target)
	fmt.Fprintf(w, "  Backend: %s\n", plan.Backend)
	fmt.Fprintf(w, "  VCS command: %s\n", plan.VCSCommand)
	if plan.UncommittedChanges > 0 {
		fmt.Fprintf(w, "  Uncommitted changes: %d\n", plan.UncommittedChanges)
	}
	if len(plan.DetachedPaths) > 0 {
		fmt.Fprintf(w, "  Detached files: %s\n", strings.Join(plan.DetachedPaths, ", "))
	}
	if plan.Refusal != "" {
		fmt.Fprintf(w, "\nRefusal: %s\n\n(--force overrides, then proceeds)\n", plan.Refusal)
	}
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show the plan without executing")
	removeCmd.Flags().BoolVarP(&removeYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	removeCmd.Flags().BoolVar(&removeForce, "force", false,
		"Override refusals and skip the prompt")
}
