package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"wrk/internal/engine"
)

var statusAll bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of managed resources (read-only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		options := engine.Options{
			StorageRoot: storageRoot,
			Stdout:      os.Stdout,
		}

		var rows []engine.ResourceStatus
		if statusAll {
			rows, err = engine.StatusAll(repo, options)
		} else {
			rows, err = engine.Status(repo, options)
		}
		if err != nil {
			return err
		}

		return printStatus(os.Stdout, rows, statusAll)
	},
}

func printStatus(w *os.File, rows []engine.ResourceStatus, all bool) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()

	if all {
		fmt.Fprintln(tw, "WORKSPACE\tRESOURCE\tPATH\tSTATE\tFINGERPRINT")
		for _, r := range rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				r.WorkspaceRoot, r.Resource, r.Path, r.State, short(r.Fingerprint))
		}
		return nil
	}

	fmt.Fprintln(tw, "RESOURCE\tPATH\tSTATE\tFINGERPRINT")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			r.Resource, r.Path, r.State, short(r.Fingerprint))
	}
	return nil
}

func short(fp string) string {
	if fp == "" {
		return "-"
	}
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().BoolVar(&statusAll, "all", false,
		"Show status across all workspaces")
}
