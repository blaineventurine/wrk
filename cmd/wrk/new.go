package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var newBase string

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
			return err
		}

		return engine.NewWorkspace(
			repo,
			args[0],
			newBase,
			engine.Options{
				StorageRoot: storageRoot,
				DryRun:      dryRun,
				Stdout:      os.Stdout,
			},
		)
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
}
