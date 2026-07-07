package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var listSize bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured resources and their shared storage",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		listings, err := engine.List(
			repo,
			engine.Options{
				StorageRoot: storageRoot,
				Stdout:      os.Stdout,
			},
			listSize,
		)
		if err != nil {
			return err
		}

		return printList(os.Stdout, listings, listSize)
	},
}

func printList(w *os.File, rows []engine.ResourceListing, withSize bool) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()

	header := "RESOURCE\tPATH\tFINGERPRINTED\tVARIANTS\tSHARED PATH"
	if withSize {
		header += "\tSIZE"
	}
	fmt.Fprintln(tw, header)

	for _, r := range rows {
		fp := "no"
		if r.Fingerprinted {
			fp = "yes"
		}

		line := fmt.Sprintf("%s\t%s\t%s\t%d\t%s",
			r.Resource, r.Path, fp, r.Variants, r.SharedPath)

		if withSize {
			line += "\t" + humanSize(r.Size)
		}

		fmt.Fprintln(tw, line)
	}

	return nil
}

func humanSize(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVar(&listSize, "size", false,
		"Compute and show on-disk size of shared storage (slower)")
}
