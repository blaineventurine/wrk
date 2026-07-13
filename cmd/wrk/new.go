package main

import (
	"bytes"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var (
	newBase string
	newJSON bool
)

var newCmd = &cobra.Command{
	Use:   "new <name-or-path>",
	Short: "Create and provision a new workspace",
	Long: "Create and provision a new workspace. A bare name (no path " +
		"separator) is placed next to the current workspace, so " +
		"`wrk new feature` from /proj/main lands at /proj/feature. " +
		"Explicit relative paths (./foo, ../foo, foo/bar) and absolute " +
		"paths are resolved literally. The destination must not sit " +
		"inside any existing workspace.\n\n" +
		"The new workspace is based on the CURRENT worktree's HEAD (or " +
		"@, for jj). Run this command from any worktree — primary or " +
		"secondary — to fork off that worktree's state.\n\n" +
		"Pass --base <ref> to fork off a specific branch, tag, or " +
		"commit instead of the current HEAD. On git, a new branch " +
		"named after the destination path is created off <ref>. On jj, " +
		"the new workspace's @ starts on top of <ref>.",
	Args: cobra.ExactArgs(1),

	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			if newJSON {
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

		if !newJSON {
			return engine.NewWorkspace(repo, args[0], newBase, options)
		}

		// --json: resolve the destination and preview the primary link
		// plan up front (both read-only) so the envelope carries them,
		// then run with stdout captured — plain-text chatter surfaces
		// in the envelope's warnings array instead of polluting the
		// machine-readable stream. `wrk new` never prompts (it creates
		// rather than destroys), so no --yes gate applies.
		runNew := func() error {
			dest, err := repo.ResolveDestination(args[0])
			if err != nil {
				return err
			}

			primaryPlan, err := engine.BuildLinkPlan(repo, options)
			if err != nil {
				return err
			}

			var warningsBuf bytes.Buffer
			options.Stdout = &warningsBuf

			if err := engine.NewWorkspace(repo, args[0], newBase, options); err != nil {
				return err
			}

			data, err := engine.MarshalNewJSON(engine.NewJSONInput{
				Destination: dest,
				Base:        newBase,
				PrimaryPlan: primaryPlan,
				DryRun:      dryRun,
				Created:     !dryRun,
				Warnings:    scanWarnings(&warningsBuf),
			})
			if err != nil {
				return err
			}
			return writeJSON(os.Stdout, data)
		}
		if err := runNew(); err != nil {
			emitJSONError(os.Stderr, err)
			return exitCode{code: 2}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(newCmd)

	newCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Show planned actions without creating the workspace",
	)
	newCmd.Flags().StringVar(
		&newBase,
		"base",
		"",
		"Ref/commit/tag to fork the new worktree off (default: current HEAD/@)",
	)
	newCmd.Flags().BoolVar(
		&newJSON,
		"json",
		false,
		"Emit a machine-readable JSON envelope (plan+result) instead of human output",
	)
}
