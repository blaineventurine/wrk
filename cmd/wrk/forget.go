package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/progress"
	"github.com/blaineventurine/wrk/internal/repository"
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
	forgetJSON  bool
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

		// Under --json every executor byte must be captured and
		// re-emitted inside the JSON envelope's `warnings` array so
		// the human-facing plain-text stream never mixes with the
		// machine-readable one. Progress becomes a running total the
		// result envelope carries as `bytesFreed`.
		var warningsBuf bytes.Buffer
		var bytesFreed int64
		if forgetJSON {
			options.Stdout = &warningsBuf
			options.Progress = func(n int64) { bytesFreed += n }
		}

		plan, err := engine.BuildForgetPlan(repo, options)
		if err != nil {
			return err
		}

		if forgetJSON {
			return runForgetJSON(plan, repo, options, &warningsBuf, &bytesFreed)
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

		bar := progress.New(os.Stdout, plan.TotalSize, "Forgetting")
		defer bar.Finish()
		options.Progress = bar.Add

		return engine.ExecuteForget(repo, plan, options)
	},
}

// runForgetJSON drives the --json path for `wrk forget`. The
// confirmation prompt still runs (with output suppressed) so a
// --force override still surfaces as a Proceed decision; a refusal
// error (e.g. detached-registry-entries without --force) still
// exits non-zero — the JSON output is only emitted when Confirm
// returned Proceed or Preview.
func runForgetJSON(
	plan engine.ForgetPlan,
	repo *repository.Repository,
	options engine.Options,
	warningsBuf *bytes.Buffer,
	bytesFreed *int64,
) error {
	attempted := false
	if !dryRun {
		dec, err := Confirm(ConfirmOptions{
			Yes:     forgetYes,
			Force:   forgetForce,
			DryRun:  dryRun,
			Refusal: plan.Refusal,
			Stdin:   os.Stdin,
			Stdout:  io.Discard,
		})
		if err != nil {
			return err
		}
		if dec == Proceed {
			if err := engine.ExecuteForget(repo, plan, options); err != nil {
				return err
			}
			attempted = true
		}
	}

	data, err := engine.MarshalForgetJSON(engine.ForgetJSONInput{
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
	forgetCmd.Flags().BoolVar(&forgetJSON, "json", false,
		"Emit machine-readable JSON (plan+result envelope) "+
			"instead of the human plan and progress output")
}
