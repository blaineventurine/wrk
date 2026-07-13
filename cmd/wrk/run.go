package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/repository"
)

// runYes is bound to `--yes`/`-y`. Scripts and CI (which have no TTY
// to answer a prompt) advertise consent explicitly.
//
// runForce is bound to `--force`. Run has no per-command refusal
// reason — detached-in-workspace refusals are hard errors from
// BuildRunPlan that no flag can override — so --force behaves as a
// stronger --yes, matching the other destructive commands.
var (
	runYes   bool
	runForce bool
	runJSON  bool
)

// runCmd wires the `wrk run <resource>` command to engine.BuildRunPlan +
// engine.ExecuteRunPlan — a forced re-execution of a single resource's
// initialize hook against the currently linked shared variant. The
// workspace symlink stays put, so every workspace linked to that
// variant sees the fresh content the moment the executor's atomic
// swap lands.
//
// Refused when the resource is not configured, has no initialize hook,
// or is detached in this workspace — the engine layer surfaces the
// specific error; RunE just plumbs the flags through.
var runCmd = &cobra.Command{
	Use:   "run <resource>",
	Short: "Re-run a resource's initialize hook against the current variant",
	Long: "DESTRUCTIVE. Re-executes the initialize hook against the " +
		"existing shared variant, atomically replacing its contents. Use " +
		"this to retry after fixing a broken hook, or to refresh a variant " +
		"without recomputing fingerprints.\n\n" +
		"The workspace symlink stays pointing at the same variant path — " +
		"every workspace linked to that variant sees the fresh content " +
		"immediately.\n\n" +
		"Destructive commands always print a plan first, then prompt for " +
		"confirmation on an interactive terminal. Non-interactive callers " +
		"must pass --yes. --dry-run prints the plan and exits without " +
		"touching anything.",
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

		// Under --json every executor byte must be captured and
		// re-emitted inside the JSON envelope's `warnings` array so
		// the human-facing plain-text stream never mixes with the
		// machine-readable one. Progress becomes a running total the
		// result envelope carries as `bytesFreed`.
		var warningsBuf bytes.Buffer
		var bytesFreed int64
		if runJSON {
			options.Stdout = &warningsBuf
			options.Progress = func(n int64) { bytesFreed += n }
		}

		plan, err := engine.BuildRunPlan(repo, args[0], options)
		if err != nil {
			return err
		}

		if runJSON {
			return runRunJSON(plan, repo, options, &warningsBuf, &bytesFreed)
		}

		printRunPlan(os.Stdout, plan)

		dec, err := Confirm(ConfirmOptions{
			Yes:    runYes,
			Force:  runForce,
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

		return engine.ExecuteRunPlan(repo, plan, options)
	},
}

// runRunJSON drives the --json path for `wrk run`. The confirmation
// prompt still runs (with output suppressed) so a --force override
// still surfaces as a Proceed decision; a refusal / preview yields
// an envelope with a nil result field.
func runRunJSON(
	plan engine.RunPlan,
	repo *repository.Repository,
	options engine.Options,
	warningsBuf *bytes.Buffer,
	bytesFreed *int64,
) error {
	attempted := false
	if !dryRun {
		dec, err := Confirm(ConfirmOptions{
			Yes:    runYes,
			Force:  runForce,
			DryRun: dryRun,
			Stdin:  os.Stdin,
			Stdout: io.Discard,
		})
		if err != nil {
			return err
		}
		if dec == Proceed {
			if err := engine.ExecuteRunPlan(repo, plan, options); err != nil {
				return err
			}
			attempted = true
		}
	}

	data, err := engine.MarshalRunJSON(engine.RunJSONInput{
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

// printRunPlan renders the RunPlan for the user. Kept small and
// inline here — there is no dedicated engine.PrintRunPlan because the
// plan has a fixed, terse shape (one resource, a handful of scalar
// fields) that a helper would only obscure.
func printRunPlan(w io.Writer, plan engine.RunPlan) {
	fmt.Fprintf(w, "Re-running initialize hook for %s (%s)\n\n",
		plan.Resource.Name, plan.Resource.Path)
	fmt.Fprintf(w, "  Actions: %d\n", len(plan.Actions))
	fmt.Fprintf(w, "  Hook commands: %d\n", len(plan.Commands))
	fmt.Fprintln(w)
	fmt.Fprintln(w,
		"WARNING: existing variant contents will be replaced atomically.")
}

func init() {
	rootCmd.AddCommand(runCmd)
	// --dry-run is registered per-command in this CLI (see link.go,
	// relink.go, detach.go); the package-global `dryRun` is the shared
	// binding target.
	runCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Show planned actions without executing them",
	)
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	runCmd.Flags().BoolVar(&runForce, "force", false,
		"Override refusals and skip the prompt")
	runCmd.Flags().BoolVar(&runJSON, "json", false,
		"Emit machine-readable JSON (plan+result envelope) "+
			"instead of the human plan output")
}
