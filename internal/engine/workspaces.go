package engine

import (
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/repository"
)

// WorkspaceState is the summary state of a workspace: a roll-up of the
// states of all its managed resources.
type WorkspaceState string

const (
	// WorkspaceLinked: every resource is linked (or intentionally expected
	// out-of-band).
	WorkspaceLinked WorkspaceState = "linked"

	// WorkspaceDetached: every resource has been intentionally detached.
	WorkspaceDetached WorkspaceState = "detached"

	// WorkspacePartial: some resources are linked, some are detached — a
	// deliberate mix.
	WorkspacePartial WorkspaceState = "partial"

	// WorkspacePending: no resources need attention, but at least one is
	// waiting to be initialized (has a hook that has not run).
	WorkspacePending WorkspaceState = "pending"

	// WorkspaceUnhealthy: at least one resource is in a state that needs
	// user action (conflict, stale, not-linked, absent).
	WorkspaceUnhealthy WorkspaceState = "unhealthy"

	// WorkspaceEmpty: no resources are configured (should not normally
	// happen, but keeps the roll-up total).
	WorkspaceEmpty WorkspaceState = "empty"
)

// WorkspaceSummary describes one workspace's overall state.
type WorkspaceSummary struct {
	Root      string
	IsCurrent bool
	State     WorkspaceState

	// Counts of each resource state within this workspace.
	Counts map[State]int
}

// WorkspaceSummaries returns a per-workspace roll-up of resource states
// across every live workspace/worktree of the repository.
//
// It never mutates anything.
func WorkspaceSummaries(
	repo *repository.Repository,
	options Options,
) ([]WorkspaceSummary, error) {
	report, err := StatusAll(repo, options)
	if err != nil {
		return nil, err
	}

	// Preserve the order in which workspaces first appear.
	order := []string{}
	byRoot := map[string][]ResourceStatus{}

	for _, r := range report.Rows {
		if _, seen := byRoot[r.WorkspaceRoot]; !seen {
			order = append(order, r.WorkspaceRoot)
		}
		byRoot[r.WorkspaceRoot] = append(byRoot[r.WorkspaceRoot], r)
	}

	current, _ := filepath.Abs(repo.Root)

	summaries := make([]WorkspaceSummary, 0, len(order))
	for _, root := range order {
		summaries = append(summaries, summarize(root, byRoot[root], current))
	}

	return summaries, nil
}

func summarize(root string, rows []ResourceStatus, current string) WorkspaceSummary {
	counts := map[State]int{}
	for _, r := range rows {
		counts[r.State]++
	}

	abs, _ := filepath.Abs(root)

	return WorkspaceSummary{
		Root:      root,
		IsCurrent: abs == current,
		State:     rollup(rows, counts),
		Counts:    counts,
	}
}

// rollup collapses per-resource states into a single workspace-level state.
//
// Priority: unhealthy > pending > (linked | detached | partial | empty).
func rollup(rows []ResourceStatus, counts map[State]int) WorkspaceState {
	if len(rows) == 0 {
		return WorkspaceEmpty
	}

	// Any unhealthy resource dominates. Missing counts as unhealthy
	// because a workspace whose shared side exists but has never been
	// linked into the workspace is not yet usable — `wrk status` shows
	// the row red per-resource, so the rollup MUST match rather than
	// falsely reporting a green WorkspaceLinked.
	if counts[StateConflict]+
		counts[StateStale]+
		counts[StateNotLinked]+
		counts[StateAbsent]+
		counts[StateMissing] > 0 {
		return WorkspaceUnhealthy
	}

	// Otherwise, a pending initialization is the most actionable state.
	if counts[StatePending] > 0 {
		return WorkspacePending
	}

	// Healthy states only from here.
	linked := counts[StateLinked] + counts[StateExpected]
	detached := counts[StateDetached]

	switch {
	case detached == 0:
		return WorkspaceLinked
	case linked == 0:
		return WorkspaceDetached
	default:
		return WorkspacePartial
	}
}
