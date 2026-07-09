package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

// forgetYes is bound to `--yes`/`-y`. Scripts and CI (which have no
// TTY to answer a prompt) advertise consent explicitly.
//
// forgetForce is bound to `--force`. It behaves as a stronger --yes:
// it skips the prompt AND overrides the soft refusal reason that
// BuildForgetPlan populates (detached-file registry entries).
var (
	forgetYes   bool
	forgetForce bool
)

var forgetCmd = &cobra.Command{
	Use:   "forget",
	Short: "Discard all shared storage and clear the detach registry",
	Long: "Permanently remove all shared storage for this repository and clear " +
		"the detach-file registry. The command removes <storage>/<repo-id>/ and " +
		"every entry in the detached-files registry for this repo.\n\n" +
		"Untouched: `.wrk.yml`, working files, and VCS metadata (git `.git`, " +
		"jj `.jj` directory, git worktree state, jj workspace state).\n\n" +
		"Destructive commands always print a plan first, then prompt for " +
		"confirmation on an interactive terminal. Non-interactive callers must " +
		"pass --yes. Refusal reasons — detached-file registry entries — require " +
		"--force to override. --dry-run prints the plan and exits without " +
		"touching anything.",
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

		plan, err := engine.BuildForgetPlan(repo, options)
		if err != nil {
			return err
		}

		printForgetPlan(os.Stdout, plan)

		dec, err := Confirm(ConfirmOptions{
			Yes:     forgetYes,
			Force:   forgetForce,
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

		return engine.ExecuteForget(repo, plan, options)
	},
}

// printForgetPlan renders the ForgetPlan for the user. Kept inline here
// — there is no dedicated engine.PrintForgetPlan because the plan has a
// fixed, terse shape that a helper would only obscure.
func printForgetPlan(w *os.File, plan engine.ForgetPlan) {
	fmt.Fprintf(w, "Forgetting repository: %s\n\n", plan.RepositoryID)
	fmt.Fprintf(w, "  Storage: %s\n", plan.StoragePath)
	fmt.Fprintf(w, "  Variants: %d across %d resource(s)\n", plan.VariantCount, plan.ResourceCount)
	fmt.Fprintf(w, "  Total size: %s\n", engine.HumanSize(plan.TotalSize))

	fmt.Fprintf(w, "\n  Registry entries: %d\n", len(plan.RegistryEntries))
	if len(plan.RegistryEntries) > 0 {
		fmt.Fprintf(w, "  Workspaces with detached files:\n")
		// Print roots sorted for deterministic output
		var roots []string
		for root := range plan.RegistryEntries {
			roots = append(roots, root)
		}
		for _, root := range roots {
			paths := plan.RegistryEntries[root]
			fmt.Fprintf(w, "    - %s (%s)\n", root, fmt.Sprint(paths))
		}
	}

	if plan.Refusal != "" {
		fmt.Fprintf(w, "\nRefusal: %s\n\n(--force overrides, then proceeds)\n", plan.Refusal)
	}
}

func init() {
	rootCmd.AddCommand(forgetCmd)
	forgetCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show the plan without executing")
	forgetCmd.Flags().BoolVarP(&forgetYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	forgetCmd.Flags().BoolVar(&forgetForce, "force", false,
		"Override refusals and skip the prompt")
}
