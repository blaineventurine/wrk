package main

import (
	"os"

	"github.com/spf13/cobra"

	"wrk/internal/engine"
)

var relinkCmd = &cobra.Command{
	Use:   "relink",
	Short: "Discard independent local copies and reconnect to shared storage",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		return engine.Relink(repo, engine.Options{
			StorageRoot: storageRoot,
			DryRun:      dryRun,
			Stdout:      os.Stdout,
		})
	},
}

func init() {
	rootCmd.AddCommand(relinkCmd)
	relinkCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Show planned actions without executing them")
}
