package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var detachCmd = &cobra.Command{
	Use:   "detach",
	Short: "Replace managed symlinks with independent workspace copies",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		return engine.Detach(
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
	rootCmd.AddCommand(detachCmd)

	detachCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Show planned actions without executing them",
	)
}
