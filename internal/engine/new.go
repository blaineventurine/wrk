package engine

import (
	"fmt"

	"github.com/blaineventurine/wrk/internal/repository"
)

// NewWorkspace creates and provisions a new workspace.
//
// Destination validation runs first so an invalid destination doesn't
// provision the primary as a surprising side effect. A clean primary
// (empty plan) is left untouched to avoid piggy-backing on `wrk new`.
//
// With Options.DryRun set, the second Link is skipped (no on-disk
// workspace to plan against).
func NewWorkspace(
	repo *repository.Repository,
	destination string,
	options Options,
) error {
	dest, err := repo.ResolveDestination(destination)
	if err != nil {
		return err
	}

	primaryPlan, err := BuildLinkPlan(repo, options)
	if err != nil {
		return err
	}
	if primaryPlan.HasConflicts() || len(primaryPlan.Actions) > 0 {
		if err := Link(repo, options); err != nil {
			return err
		}
	}

	if options.DryRun {
		fmt.Fprintln(options.Stdout)
		fmt.Fprintf(options.Stdout, "Would create workspace at %s\n", dest)
		fmt.Fprintln(options.Stdout,
			"(dry-run: second Link cannot be previewed until the workspace exists)")
		return nil
	}

	newRepo, err := repo.CreateWorkspace(destination)
	if err != nil {
		return err
	}

	return Link(
		newRepo,
		options,
	)
}
