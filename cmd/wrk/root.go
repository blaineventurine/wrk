package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/app"
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
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("{{ .Version }}\n")
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

	rootCmd.PersistentFlags().BoolVar(
		&noColor, "no-color", false,
		"Disable ANSI color output (also respects the NO_COLOR env var)",
	)
}
