package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

var listSize bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured resources and their shared storage",
	Long: "List every configured resource and the shared-storage location " +
		"it links to. Read-only. Use --size to walk each shared tree and " +
		"report on-disk usage; this is noticeably slower for large caches " +
		"such as node_modules.",
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

func printList(w io.Writer, rows []engine.ResourceListing, withSize bool) error {
	showOrigin := hasNonSharedListOrigin(rows)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', tabwriter.StripEscape)
	defer func() {
		_ = tw.Flush()
	}()

	header := "RESOURCE\tPATH\tFINGERPRINTED\tVARIANTS\tSHARED PATH"
	if showOrigin {
		header = "RESOURCE\tPATH\tFINGERPRINTED\tVARIANTS\tORIGIN\tSHARED PATH"
	}
	if withSize {
		header += "\tSIZE"
	}
	_, _ = fmt.Fprintln(tw, header)

	for _, r := range rows {
		fp := "no"
		if r.Fingerprinted {
			fp = "yes"
		}

		fields := []string{r.Resource, r.Path, fp, fmt.Sprintf("%d", r.Variants)}
		if showOrigin {
			fields = append(fields, string(r.Origin))
		}
		fields = append(fields, r.SharedPath)
		if withSize {
			fields = append(fields, engine.HumanSize(r.Size))
		}

		_, _ = fmt.Fprintln(tw, strings.Join(fields, "\t"))
	}

	return nil
}

func hasNonSharedListOrigin(rows []engine.ResourceListing) bool {
	for _, r := range rows {
		if r.Origin != config.OriginShared {
			return true
		}
	}
	return false
}


func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVar(&listSize, "size", false,
		"Compute and show on-disk size of shared storage (slower)")
}
