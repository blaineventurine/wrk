package main

import (
	"errors"
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

// exitCode is a sentinel error returned by commands that want to
// signal a non-error exit status (for example `wrk status --exit-code`
// when it finds problems). Its Error() is empty so nothing is printed
// to stderr; Execute checks for the type and calls os.Exit(e.code).
type exitCode struct{ code int }

func (e exitCode) Error() string { return "" }

// Execute runs the root command. Exit codes:
//
//	0  success
//	1  `wrk status --exit-code` found problems (via exitCode sentinel)
//	2  any other error (bad flags, missing repo, config load failure, ...)
//
// Real errors are printed to stderr; the --exit-code signal is silent
// because the status table above has already told the user what's
// wrong.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func init() {
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("{{ .Version }}\n")
	rootCmd.PersistentFlags().StringVar(
		&vcs,
		"vcs",
		"auto",
		"Repository type: 'auto' (prefers jj in colocated repos), 'git', "+
			"or 'jj' — override auto-detection.",
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
