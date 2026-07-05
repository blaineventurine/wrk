package main

import (
	"os"

	"github.com/spf13/cobra"

	"wrk/internal/engine"
)

var linkCmd = &cobra.Command{
	Use:   "link",
	Short: "Initialize or repair the current workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		return engine.Link(
			repo,
			engine.Options{
				StorageRoot: storageRoot,
				DryRun:      dryRun,
				Stdout:      os.Stdout,
			},
		)
	},
}

func init() {
	rootCmd.AddCommand(linkCmd)

	linkCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Show planned actions without executing them",
	)
}
