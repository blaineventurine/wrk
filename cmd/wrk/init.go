package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/blaineventurine/wrk/internal/engine"
)

// initForce is bound to `--force`. Present so a user with a
// pre-existing `.wrk.yml` can regenerate it without moving the file
// out of the way.
//
// initYes is bound to `--yes`/`-y`. Only meaningful under --force
// against a repo whose `.wrk.yml` already exists: the CLI prints
// "Overwriting: <path>" and prompts for confirmation on a TTY, and
// --yes/--force skip the prompt (matching every other destructive
// command).
var (
	initForce bool
	initYes   bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a starter .wrk.yml for the current repository",
	Long: "Inspect the repository root for well-known project files " +
		"(package.json, Gemfile, pyproject.toml, ...) and write a .wrk.yml " +
		"seeded with sensible defaults. Must be run inside a git or jj " +
		"repository — the file is always written at the repository root, " +
		"not the current working directory. Refuses to overwrite an " +
		"existing file unless --force is passed.\n\n" +
		"When --force overwrites an existing .wrk.yml the CLI prints " +
		"'Overwriting: <path>' first and prompts for confirmation on an " +
		"interactive terminal. Non-interactive callers must pass --yes.",
	Args: cobra.NoArgs,

	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := currentRepository()
		if err != nil {
			return err
		}

		target := filepath.Join(repo.Root, ".wrk.yml")

		// --force against an existing .wrk.yml is the ONLY destructive
		// branch of this command (a fresh init writes a brand-new file
		// via O_EXCL). Print the plan-shaped "Overwriting" line and
		// gate on Confirm so the user gets the same explicit-consent
		// UX every other destructive command provides.
		if initForce && !dryRun {
			// Lstat, not Stat: a symlinked .wrk.yml whose target is
			// broken would otherwise slip past `os.Stat` (which
			// follows the link) and skip the prompt — the next
			// engine.Init.WriteFile would then follow the link and
			// silently create a file at the (unrelated) target path
			// while leaving the symlink itself in place. Lstat sees
			// the link itself so the prompt fires.
			if info, statErr := os.Lstat(target); statErr == nil {
				fmt.Fprintf(os.Stdout, "Overwriting existing config: %s\n\n", target)

				dec, err := Confirm(ConfirmOptions{
					Yes: initYes,
					// initForce is a permission flag ("allow
					// overwrite"), not a consent flag — a user who
					// typed --force still deserves the prompt so a
					// stray shell history entry doesn't nuke their
					// config. --yes/-y is the consent flag.
					Force:  false,
					DryRun: false,
					Stdin:  os.Stdin,
					Stdout: os.Stdout,
				})
				if err != nil {
					return err
				}
				if dec != Proceed {
					return nil
				}
				// If the pre-existing entry was NOT a plain regular
				// file (symlink, socket, whatever the user set up),
				// remove it before engine.Init's WriteFile runs so
				// the result is deterministically a fresh regular
				// file at .wrk.yml. Otherwise WriteFile through a
				// symlink writes the target, and the user's `--yes`
				// silently touches an unrelated path while the
				// symlink survives.
				if !info.Mode().IsRegular() {
					if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
						return fmt.Errorf("remove non-regular %s before overwrite: %w", target, err)
					}
				}
			}
		}

		return engine.Init(engine.InitOptions{
			Root:   repo.Root,
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
	initCmd.Flags().BoolVarP(
		&initYes,
		"yes", "y", false,
		"Skip the overwrite confirmation prompt (implies --force is safe to apply)",
	)

	initCmd.Flags().BoolVar(
		&dryRun,
		"dry-run", false,
		"Print the generated .wrk.yml to stdout without writing it",
	)
}
