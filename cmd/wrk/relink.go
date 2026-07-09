package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

// relinkYes is bound to `--yes`/`-y`. When set, relink runs without
// asking the user to confirm the destructive action.
var relinkYes bool

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
		"Because this action has no undo, `wrk relink` requires explicit " +
		"consent when it will actually execute: pass --yes (-y) to skip " +
		"the prompt, or answer `y` at the interactive confirmation. " +
		"Non-interactive invocations (pipes, CI) without --yes refuse to " +
		"run. --dry-run bypasses confirmation entirely because nothing " +
		"is written.\n\n" +
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

		if relinkIsolate {
			if !dryRun {
				if err := confirmRelinkIsolate(
					relinkYes, os.Stdin, os.Stdout,
				); err != nil {
					return err
				}
			}
			return engine.RelinkIsolate(repo, args, engine.Options{
				StorageRoot: storageRoot,
				DryRun:      dryRun,
				Stdout:      os.Stdout,
			})
		}

		if !dryRun {
			if err := confirmRelink(relinkYes, os.Stdin, os.Stdout); err != nil {
				return err
			}
		}

		return engine.Relink(repo, engine.Options{
			StorageRoot: storageRoot,
			DryRun:      dryRun,
			Stdout:      os.Stdout,
		})
	},
}

// confirmRelink gates a destructive `wrk relink` invocation.
//
// Behaviour matrix (dry-run is handled by the caller — dry-run skips
// confirmation entirely because nothing is written):
//
//   - yes==true                       → proceed silently.
//   - stdin is not a TTY, yes==false  → refuse; there is no one to
//     answer the prompt, so demand --yes explicitly.
//   - stdin is a TTY,     yes==false  → print a warning banner and
//     read one line from stdin; accept "y"/"yes" (any case) as
//     consent, otherwise abort.
//
// The abort path returns a fresh error (not the exitCode sentinel) so
// the top-level Execute prints it to stderr and exits 2 — this is a
// real user-facing "no, I won't do that" message, not a silent signal.
func confirmRelink(yes bool, in *os.File, out *os.File) error {
	if yes {
		return nil
	}
	if !isatty.IsTerminal(in.Fd()) {
		return errors.New(
			"refusing to run destructive relink without --yes; " +
				"re-run with `wrk relink --yes` to confirm",
		)
	}

	fmt.Fprintln(out,
		"This will discard independent local copies made by "+
			"`wrk detach` — no undo.")
	fmt.Fprint(out, "Continue? [y/N]: ")

	// bufio.ReadString returns io.EOF (and any partial line) when the
	// pipe closes before a newline. We don't need to special-case:
	// whatever we got (possibly empty) flows into the answer check
	// below, and an empty or non-"y" answer aborts.
	reader := bufio.NewReader(in)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return errors.New("aborted")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(relinkCmd)
	relinkCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show planned actions without executing them")
	relinkCmd.Flags().BoolVarP(&relinkYes, "yes", "y", false,
		"Skip the destructive-action confirmation prompt")
	relinkCmd.Flags().BoolVar(&relinkIsolate, "isolate", false,
		"Promote detached resources into per-workspace variants "+
			"(does not discard local edits)")
}

// confirmRelinkIsolate gates the `wrk relink --isolate` flow. This
// operation is less destructive than a plain relink — the user's
// detached bytes are preserved verbatim, just moved into a private
// variant under shared storage — but it still changes what peer
// workspaces see, so we prompt for consent.
//
// Behaviour matrix mirrors confirmRelink:
//
//   - yes==true                       → proceed silently.
//   - stdin is not a TTY, yes==false  → refuse and demand --yes.
//   - stdin is a TTY,     yes==false  → print a softer warning banner
//     and read one line from stdin; "y"/"yes" (any case) consents,
//     anything else aborts.
func confirmRelinkIsolate(yes bool, in *os.File, out *os.File) error {
	if yes {
		return nil
	}
	if !isatty.IsTerminal(in.Fd()) {
		return errors.New(
			"--yes required when stdin is not a TTY; " +
				"re-run with `wrk relink --isolate --yes` to confirm",
		)
	}

	fmt.Fprintln(out,
		"This will move your detached files into a private variant "+
			"in shared storage.\n"+
			"Peer workspaces will not see the content. To reconnect "+
			"later, run `wrk relink`.")
	fmt.Fprint(out, "Continue? [y/N]: ")

	// See confirmRelink for the io.EOF rationale — an empty or non-"y"
	// reply aborts, so we don't need to special-case pipe closure.
	reader := bufio.NewReader(in)
	line, _ := reader.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return errors.New("aborted")
	}
	return nil
}
