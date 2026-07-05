package main

import (
	"os"

	"github.com/spf13/cobra"

	"wrk/internal/engine"
)

var newCmd = &cobra.Command{
	Use:   "new <directory>",
	Short: "Create and provision a new workspace",
	Args:  cobra.ExactArgs(1),

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
