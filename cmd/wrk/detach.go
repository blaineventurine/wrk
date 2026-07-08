package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var detachCmd = &cobra.Command{
	Use:   "detach",
	Short: "Replace managed symlinks with independent workspace copies",
	Long: "Replace every managed symlink with an independent copy that " +
		"lives inside this workspace, so subsequent edits stay local. " +
		"Records which resources were detached so `wrk status` can " +
		"distinguish a deliberate detach from an accidental conflict. " +
		"Reverse safely with `wrk link` (keeps your local changes) or " +
		"destructively with `wrk relink` (discards them).",
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
