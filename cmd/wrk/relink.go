package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/repository"
)

// relinkYes is bound to `--yes`/`-y`. When set, relink runs without
// asking the user to confirm the destructive action.
//
// relinkForce is bound to `--force`. It behaves as a stronger --yes
// for symmetry with the other destructive commands; relink has no
// per-command refusal reason (BuildRelinkPlan is a pure plan builder
// and the --isolate preflight failures are hard errors), so --force
// just skips the prompt like --yes.
var (
	relinkYes   bool
	relinkForce bool
)

// relinkIsolate is bound to `--isolate`. When set, relink switches from
// the "discard local copies and reconnect to shared" flow to
// "promote local copies into a private per-workspace variant" — see
// engine.RelinkIsolate.
var relinkIsolate bool

var relinkCmd = &cobra.Command{
	Use:   "relink [resource...]",
	Short: "Discard independent local copies and reconnect to shared storage (or --isolate)",
	Long: "DESTRUCTIVE. Discards the independent local copies produced by " +
		"`wrk detach` and reconnects this workspace to shared storage. " +
		"Any local edits made since the detach are lost — preview first " +
		"with --dry-run. Prefer `wrk link` if you want to keep your local " +
		"changes.\n\n" +
		"Destructive commands always print a plan first, then prompt for " +
		"confirmation on an interactive terminal. Non-interactive callers " +
		"must pass --yes. --dry-run bypasses confirmation entirely because " +
		"nothing is written.\n\n" +
		"With --isolate, relink instead promotes the detached copies into " +
		"private per-workspace variants under shared storage: the files " +
		"are preserved (not discarded), but they become invisible to peer " +
		"workspaces. Pass one or more resource names to isolate a subset; " +
		"with no names, every currently-detached resource in this " +
		"workspace is isolated.",
	// Positional resource arguments are only meaningful with --isolate.
	// Reject them on the default path so a typo like `wrk relink node`
	// doesn't silently discard a workspace's detached bytes across
	// every resource.
	Args: func(cmd *cobra.Command, args []string) error {
		if !relinkIsolate && len(args) > 0 {
			return errors.New(
				"positional resource arguments are only valid with --isolate")
		}
		return nil
	},
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

		if relinkIsolate {
			return runRelinkIsolate(repo, args, options)
		}
		return runRelinkPlain(repo, options)
	},
}

// runRelinkPlain drives the Build -> Print -> Confirm -> Execute
// flow for `wrk relink` (no --isolate). The prompt sits between the
// plan preview and the destructive move so a user who reads the plan
// and changes their mind can bail without having lost anything.
func runRelinkPlain(repo *repository.Repository, options engine.Options) error {
	plan, err := engine.BuildRelinkPlan(repo, options)
	if err != nil {
		return err
	}

	if err := engine.PrintPlan(os.Stdout, plan); err != nil {
		return err
	}

	dec, err := Confirm(ConfirmOptions{
		Yes:    relinkYes,
		Force:  relinkForce,
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

	return engine.ExecuteRelink(repo, plan, options)
}

// runRelinkIsolate drives the Build -> Print -> Confirm -> Execute
// flow for `wrk relink --isolate`. The plan preview uses the local
// printIsolatePlan since the IsolatePlan struct is not a planner.Plan.
func runRelinkIsolate(repo *repository.Repository, resourceNames []string, options engine.Options) error {
	plan, err := engine.BuildRelinkIsolatePlan(repo, resourceNames, options)
	if err != nil {
		return err
	}

	printIsolatePlan(os.Stdout, plan)

	dec, err := Confirm(ConfirmOptions{
		Yes:    relinkYes,
		Force:  relinkForce,
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

	return engine.ExecuteRelinkIsolate(repo, plan, options)
}

// printIsolatePlan renders the IsolatePlan for the user. Kept small
// and inline: the plan's user-visible fields are just a list of
// resources, so a dedicated engine.PrintIsolatePlan would only
// obscure the layout.
func printIsolatePlan(w io.Writer, plan engine.IsolatePlan) {
	fmt.Fprintln(w,
		"Isolating detached resources into per-workspace variants:")
	for _, r := range plan.Resources {
		fmt.Fprintf(w, "  - %s (%s)\n", r.Name, r.Path)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w,
		"Files stay under shared storage but become invisible to peer workspaces.")
}

func init() {
	rootCmd.AddCommand(relinkCmd)
	relinkCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show planned actions without executing them")
	relinkCmd.Flags().BoolVarP(&relinkYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	relinkCmd.Flags().BoolVar(&relinkForce, "force", false,
		"Override refusals and skip the prompt")
	relinkCmd.Flags().BoolVar(&relinkIsolate, "isolate", false,
		"Promote detached resources into per-workspace variants "+
			"(does not discard local edits)")
}
