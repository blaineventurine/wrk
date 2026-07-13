package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/progress"
	"github.com/blaineventurine/wrk/internal/repository"
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
	removeJSON  bool
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
			if removeJSON {
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
		if removeJSON {
			options.Stdout = &warningsBuf
			options.Progress = func(n int64) { bytesFreed += n }
		}

		plan, err := engine.BuildRemovePlan(repo, args[0], options)
		if err != nil {
			if removeJSON {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return err
		}

		if removeJSON {
			if err := runRemoveJSON(plan, repo, options, &warningsBuf, &bytesFreed); err != nil {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return nil
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

		// Only the jj backend emits per-file byte events during a
		// remove: `git worktree remove` deletes inside its own
		// subprocess so wrk cannot inspect its progress. Rather than
		// render a bar that snaps from 0% to 100% at the end for
		// git, skip creation entirely on that backend — the plan
		// display still shows the pre-remove Size so the user knows
		// what is disappearing.
		if plan.Backend == "jj" {
			bar := progress.New(os.Stdout, plan.TotalBytes, "Removing")
			defer bar.Finish()
			options.Progress = bar.Add
		}

		return engine.ExecuteRemove(repo, plan, removeForce, options)
	},
}

// runRemoveJSON drives the --json path for `wrk remove`. The
// confirmation prompt still runs (with output suppressed) so a
// --force override still surfaces as a Proceed decision; a refusal
// error (e.g. uncommitted-changes without --force) still exits
// non-zero — the JSON output is only emitted when Confirm returned
// Proceed or Preview.
func runRemoveJSON(
	plan engine.RemovePlan,
	repo *repository.Repository,
	options engine.Options,
	warningsBuf *bytes.Buffer,
	bytesFreed *int64,
) error {
	attempted := false
	if !dryRun {
		dec, err := Confirm(ConfirmOptions{
			Yes:     removeYes,
			Force:   removeForce,
			DryRun:  dryRun,
			Refusal: plan.Refusal,
			Stdin:   os.Stdin,
			Stdout:  io.Discard,
		})
		if err != nil {
			return err
		}
		if dec == Proceed {
			if err := engine.ExecuteRemove(repo, plan, removeForce, options); err != nil {
				return err
			}
			attempted = true
		}
	}

	data, err := engine.MarshalRemoveJSON(engine.RemoveJSONInput{
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

// printRemovePlan renders the RemovePlan for the user. Kept small
// and inline here — there is no dedicated engine.PrintRemovePlan
// because the plan has a fixed, terse shape (one target, a handful
// of scalar fields) that a helper would only obscure.
func printRemovePlan(w *os.File, plan engine.RemovePlan) {
	fmt.Fprintf(w, "Removing workspace: %s\n\n", plan.Target)
	fmt.Fprintf(w, "  Backend: %s\n", plan.Backend)
	fmt.Fprintf(w, "  VCS command: %s\n", plan.VCSCommand)
	fmt.Fprintf(w, "  Size: %s\n", engine.HumanSize(plan.TotalBytes))
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
	removeCmd.Flags().BoolVar(&removeJSON, "json", false,
		"Emit machine-readable JSON (plan+result envelope) "+
			"instead of the human plan and progress output")
}
