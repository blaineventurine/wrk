package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var newCmd = &cobra.Command{
	Use:   "new <name-or-path>",
	Short: "Create and provision a new workspace",
	Long: "Create and provision a new workspace. A bare name (no path " +
		"separator) is placed next to the current workspace, so " +
		"`wrk new feature` from /proj/main lands at /proj/feature. " +
		"Explicit relative paths (./foo, ../foo, foo/bar) and absolute " +
		"paths are resolved literally. The destination must not sit " +
		"inside any existing workspace.",
	Args: cobra.ExactArgs(1),

	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		return engine.NewWorkspace(
			repo,
			args[0],
			engine.Options{
				StorageRoot: storageRoot,
				Stdout:      os.Stdout,
			},
		)
	},
}

func init() {
	rootCmd.AddCommand(newCmd)
}
