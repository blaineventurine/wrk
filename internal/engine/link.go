package engine

import (
	"errors"
	"fmt"

	"github.com/blaineventurine/wrk/internal/executor"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// Link initializes or repairs the current workspace.
func Link(repo *repository.Repository, options Options) error {
	plan, err := BuildLinkPlan(repo, options)
	if err != nil {
		return err
	}

	if err := runPlan(plan, options); err != nil {
		return err
	}

	if options.DryRun {
		return nil
	}

	// A successful link reconnects the workspace to shared storage, so
	// clear any prior detach record.
	if err := clearDetached(repo); err != nil {
		return fmt.Errorf("link succeeded but failed to clear detach record: %w", err)
	}
	return nil
}

// Relink reconnects the current workspace to shared storage, discarding any
// independent local copies created by a previous `detach`.
//
// Relink is the plan-then-execute wrapper preserved for backward
// compatibility. It prints the plan before executing. CLI callers that
// need to interpose confirmation should call BuildRelinkPlan +
// ExecuteRelink directly instead — Relink would double-print.
func Relink(repo *repository.Repository, options Options) error {
	plan, err := BuildRelinkPlan(repo, options)
	if err != nil {
		return err
	}
	if err := printPlan(options.Stdout, plan); err != nil {
		return err
	}
	return ExecuteRelink(repo, plan, options)
}

// ExecuteRelink runs a pre-built relink plan without printing it. On
// success (and outside dry-run) the workspace's detach-registry entry
// is cleared so `wrk status` no longer reports the resources as
// detached.
//
// Callers that print the plan themselves — e.g. the CLI's Build ->
// Print -> Confirm -> Execute flow — must use this instead of Relink
// to avoid a double-print.
func ExecuteRelink(repo *repository.Repository, plan planner.Plan, options Options) error {
	if err := executePlan(plan, options); err != nil {
		return err
	}

	if options.DryRun {
		return nil
	}

	if err := clearDetached(repo); err != nil {
		return fmt.Errorf("relink succeeded but failed to clear detach record: %w", err)
	}
	return nil
}

// runPlan prints, validates, and (unless dry-run) executes a plan.
// Kept for callers (currently `Link`, `Run`, and the monolithic
// `Detach`/`Relink` wrappers above) that want the classic
// print-then-execute path in a single call. New CLI code SHOULD prefer
// the Build -> Print -> Confirm -> ExecuteX split so `Confirm` can slot
// between planning and execution.
func runPlan(plan planner.Plan, options Options) error {
	if err := printPlan(options.Stdout, plan); err != nil {
		return err
	}
	return executePlan(plan, options)
}

// executePlan is the print-free half of runPlan. CLI commands that
// already ran the Confirm dance (and therefore printed the plan
// themselves) use this to avoid a double-print.
func executePlan(plan planner.Plan, options Options) error {
	if plan.HasConflicts() {
		return fmt.Errorf(
			"%d conflict(s) — see plan output above",
			len(plan.Conflicts),
		)
	}

	if options.DryRun {
		return nil
	}

	if err := executor.Execute(plan); err != nil {
		// A failing initialize hook is the single most common
		// destructive-command failure mode; route it to a stable
		// code so `wrk <cmd> --json` surfaces `hook_command_failed`
		// instead of the fallback `unknown`. errors.As traverses
		// through fmt.Errorf wraps, so future callers that add
		// context via %w on either side stay routable. The wrap
		// preserves the executor's wording verbatim in Message
		// (Wrapf collapses the mirror case in *Error.Error) and
		// keeps HookError on the Unwrap chain so downstream
		// errors.As on *HookError still works.
		var hookErr *executor.HookError
		if errors.As(err, &hookErr) {
			return Wrapf(ErrHookCommandFailed,
				"check the hook's stderr above for details",
				err, "%s", err.Error())
		}
		return err
	}
	return nil
}
