package main

import (
	"bytes"
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
// --yes skips the prompt (matching every other destructive command).
//
// initJSON is bound to `--json`. Emits a machine-readable envelope;
// combined with an existing config it requires --force AND --yes,
// because the human path would prompt and a --json caller has no TTY
// contract.
var (
	initForce bool
	initYes   bool
	initJSON  bool
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
			if initJSON {
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return err
		}

		target := filepath.Join(repo.Root, ".wrk.yml")

		if initJSON {
			if err := runInitJSON(repo.Root, target); err != nil {
				if ec, ok := err.(exitCode); ok {
					return ec
				}
				emitJSONError(os.Stderr, err)
				return exitCode{code: 2}
			}
			return nil
		}

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
				if err := removeNonRegularConfig(target, info); err != nil {
					return err
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

// runInitJSON drives the --json path for `wrk init`. The consent
// matrix mirrors the human path without ever prompting:
//
//   - no existing config       → write, emit envelope.
//   - --dry-run                → emit envelope with content, write nothing.
//   - exists, no --force       → structured error (exit 2).
//   - exists, --force, no --yes → structured error: --json cannot
//     answer an interactive overwrite prompt, so explicit --yes is
//     required (same contract as every other destructive command).
func runInitJSON(root, target string) error {
	detected, content := engine.InitPreview(root)

	info, statErr := os.Lstat(target)
	exists := statErr == nil

	if dryRun {
		data, err := engine.MarshalInitJSON(engine.InitJSONInput{
			Path:     target,
			Detected: detected,
			Exists:   exists,
			Content:  content,
			DryRun:   true,
		})
		if err != nil {
			return err
		}
		return writeJSON(os.Stdout, data)
	}

	if exists {
		if !initForce {
			return fmt.Errorf(
				"%s already exists — use --force to overwrite", target,
			)
		}
		if !initYes {
			return engine.Newf(engine.ErrJSONRequiresYes,
				"combine --json --force with --yes to overwrite, or --dry-run to preview",
				"--json overwrite of an existing %s requires --yes to skip the confirmation prompt",
				target)
		}
		if err := removeNonRegularConfig(target, info); err != nil {
			return err
		}
	}

	var warningsBuf bytes.Buffer
	if err := engine.Init(engine.InitOptions{
		Root:   root,
		Force:  initForce,
		DryRun: false,
		Stdout: &warningsBuf,
	}); err != nil {
		return err
	}

	data, err := engine.MarshalInitJSON(engine.InitJSONInput{
		Path:     target,
		Detected: detected,
		Exists:   exists,
		Wrote:    true,
		Warnings: scanWarnings(&warningsBuf),
	})
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, data)
}

// removeNonRegularConfig removes a pre-existing non-regular entry
// (symlink, socket, ...) at target before engine.Init's WriteFile runs,
// so the result is deterministically a fresh regular file at .wrk.yml.
// Otherwise WriteFile through a symlink writes the target, silently
// touching an unrelated path while the symlink survives.
func removeNonRegularConfig(target string, info os.FileInfo) error {
	if info.Mode().IsRegular() {
		return nil
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove non-regular %s before overwrite: %w", target, err)
	}
	return nil
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
		"Skip the overwrite confirmation prompt (only meaningful with --force)",
	)

	initCmd.Flags().BoolVar(
		&dryRun,
		"dry-run", false,
		"Print the generated .wrk.yml to stdout without writing it",
	)
	initCmd.Flags().BoolVar(
		&initJSON,
		"json", false,
		"Emit a machine-readable JSON envelope instead of human output",
	)
}
