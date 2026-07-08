package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var relinkCmd = &cobra.Command{
	Use:   "relink",
	Short: "Discard independent local copies and reconnect to shared storage",
	Long: "DESTRUCTIVE. Discards the independent local copies produced by " +
		"`wrk detach` and reconnects this workspace to shared storage. " +
		"Any local edits made since the detach are lost — preview first " +
		"with --dry-run. Prefer `wrk link` if you want to keep your local " +
		"changes.",
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
