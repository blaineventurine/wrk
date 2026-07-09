package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

// runCmd wires the `wrk run <resource>` command to engine.Run — a
// forced re-execution of a single resource's initialize hook against
// the currently linked shared variant. The workspace symlink stays put,
// so every workspace linked to that variant sees the fresh content the
// moment the executor's atomic swap lands.
//
// Refused when the resource is not configured, has no initialize hook,
// or is detached in this workspace — the engine layer surfaces the
// specific error; RunE just plumbs the flags through.
var runCmd = &cobra.Command{
	Use:   "run <resource>",
	Short: "Re-run a resource's initialize hook against the current variant",
	Long: "DESTRUCTIVE. Re-executes the initialize hook against the " +
		"existing shared variant, atomically replacing its contents. Use " +
		"this to retry after fixing a broken hook, or to refresh a variant " +
		"without recomputing fingerprints.\n\n" +
		"The workspace symlink stays pointing at the same variant path — " +
		"every workspace linked to that variant sees the fresh content " +
		"immediately. Use --dry-run to preview.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		return engine.Run(repo, args[0], engine.Options{
			StorageRoot: storageRoot,
			DryRun:      dryRun,
			Stdout:      os.Stdout,
		})
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
	// --dry-run is registered per-command in this CLI (see link.go,
	// relink.go, detach.go); the package-global `dryRun` is the shared
	// binding target.
	runCmd.Flags().BoolVar(
		&dryRun,
		"dry-run",
		false,
		"Show planned actions without executing them",
	)
}
