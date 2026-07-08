package engine

import (
	"fmt"

	"github.com/blaineventurine/wrk/internal/repository"
)

// NewWorkspace creates and provisions a new workspace.
//
// Before creating the destination, the primary workspace is preflighted:
// its Link plan is built and, if it has any actions or conflicts, Link
// runs. A clean primary (empty plan) is left untouched so that unrelated
// pending work — a queued initialize hook, a stale symlink flagged as
// conflict — does not silently piggy-back on `wrk new`. Conflicts fail
// fast with the primary's own message.
//
// With Options.DryRun set, no filesystem side effects are performed:
// the primary Link runs in dry-run mode (also gated on the plan being
// non-empty), the destination is resolved and validated in-memory, and
// a summary of what would happen is printed. The second Link (against
// the not-yet-existing workspace) is skipped, because there is no
// on-disk workspace to plan against.
func NewWorkspace(
	repo *repository.Repository,
	destination string,
	options Options,
) error {
	// Preflight the primary: skip the Link entirely when its plan is a
	// no-op. Rebuilding the plan inside Link is cheap (no execution) and
	// keeps the shared entry point authoritative — buildPlan is pure, so
	// running it twice on a clean workspace has no side effects.
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
		dest, err := repo.ResolveDestination(destination)
		if err != nil {
			return err
		}
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
