package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var linkCmd = &cobra.Command{
	Use:   "link",
	Short: "Initialize or repair the current workspace",
	Long: "Create or repair the symlinks that connect this workspace to " +
		"shared storage, and run each resource's initialize hook once per " +
		"shared fingerprint. Idempotent: safe to re-run at any time. Never " +
		"clobbers a local edit — a mismatch is reported as a conflict for " +
		"you to resolve. Reversible with `wrk detach`.",
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
