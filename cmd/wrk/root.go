package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"wrk/internal/app"
)

var (
	storageRoot string
	dryRun      bool
	vcs         string
)

var rootCmd = &cobra.Command{
	Use:           "wrk",
	Short:         "Provision shared resources across workspaces",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(
		&vcs,
		"vcs",
		"auto",
		"Repository type (auto, git, jj)",
	)

	rootCmd.PersistentFlags().StringVar(
		&storageRoot,
		"storage",
		app.DefaultStorage(),
		"Shared storage location",
	)
}
