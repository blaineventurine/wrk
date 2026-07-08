package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a starter .wrk.yml for the current repository",
	Long: "Inspect the current directory for well-known project files " +
		"(package.json, Gemfile, pyproject.toml, ...) and write a .wrk.yml " +
		"seeded with sensible defaults. Refuses to overwrite an existing " +
		"file unless --force is passed.",
	Args: cobra.NoArgs,

	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		return engine.Init(engine.InitOptions{
			Root:   cwd,
			Force:  initForce,
			DryRun: dryRun,
			Stdout: os.Stdout,
		})
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().BoolVarP(
		&initForce,
		"force", "f", false,
		"Overwrite an existing .wrk.yml",
	)

	initCmd.Flags().BoolVar(
		&dryRun,
		"dry-run", false,
		"Print the generated .wrk.yml to stdout without writing it",
	)
}
