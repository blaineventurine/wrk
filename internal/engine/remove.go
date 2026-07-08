package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blaineventurine/wrk/internal/repository"
)

// RemovePlan describes the actions `wrk remove` would take. It is
// the read-only output of BuildRemovePlan: no mutation has occurred
// yet. The CLI (Task 2.4) prints it and the executor (Task 2.3)
// applies it.
//
// Refusal is the composed, human-readable reason a soft refusal
// applies (uncommitted changes and/or detached files). Multiple
// reasons are joined by "; " so a single string suffices for the
// CLI's --force decision.
type RemovePlan struct {
	// Target is the resolved, canonicalized absolute path of the
	// workspace to remove. Registry lookups and Workspaces()
	// comparisons all happen against this form.
	Target string

	// Backend is the underlying VCS as a bare string ("git" or "jj")
	// so the CLI can render it without importing the repository
	// package's enum type.
	Backend string

	// VCSCommand is the display form of the command the executor
	// will run. For git: "git worktree remove <target>"; for jj it
	// carries a <name> placeholder because jj resolves the workspace
	// NAME from the path, and that resolution lives in Task 2.2.
	VCSCommand string

	// UncommittedChanges is the count of dirty files reported by
	// `git status --porcelain` in the target. 0 means clean.
	UncommittedChanges int

	// DetachedPaths is the workspace-relative paths currently marked
	// as detached in the registry for Target. Empty when the
	// workspace has no detach entry.
	DetachedPaths []string

	// IsGhost is true when Target is NOT in Workspaces() but the
	// detach registry still carries an entry keyed by it — a stale
	// bookkeeping artefact `wrk gc` should sweep.
	IsGhost bool

	// Refusal is the composed refusal reason. Empty when no refusal
	// applies. Ghost cases populate Refusal with a hint at `wrk gc`
	// (and the caller MUST branch on IsGhost, not Refusal-emptiness,
	// to distinguish soft from ghost refusals).
	Refusal string
}

// BuildRemovePlan resolves destination and checks every refusal guard
// for `wrk remove`. It is read-only: nothing on disk, in the VCS, or
// in the registry is modified.
//
// Refusal precedence:
//
//  1. Empty / "." / ".." destination → hard error.
//  2. Target == current workspace     → hard error.
//  3. Target == primary workspace     → hard error (--force cannot
//     override; the primary is the anchor everything else hangs off).
//  4. Target not in Workspaces()      → EITHER
//     a. a stale registry entry exists → soft refusal + ghost hint
//        pointing at `wrk gc`, OR
//     b. no registry entry            → hard error ("not a live
//        workspace of this repo").
//  5. Uncommitted VCS changes         → soft refusal, --force may
//     override.
//  6. Detached-file registry entries  → soft refusal, --force may
//     override.
//
// Cases 5 and 6 can coexist; both reasons accumulate into Refusal
// separated by "; ".
//
// destination follows the same sibling-default policy as `wrk new`:
// a bare name resolves to a sibling of the primary; relative paths
// join against the primary; absolute paths pass through. Unlike
// `wrk new`, BuildRemovePlan does NOT refuse a destination that
// already exists — remove precisely WANTS it to exist.
func BuildRemovePlan(
	repo *repository.Repository,
	destination string,
	options Options,
) (RemovePlan, error) {
	// 1. Sentinel destinations collapse to r.Root or its parent —
	// reject them up front with the same wording ResolveDestination
	// uses so `wrk new` and `wrk remove` fail identically for the
	// same shape of user error.
	trimmed := strings.TrimSpace(destination)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return RemovePlan{}, fmt.Errorf("destination cannot be %q", destination)
	}

	// 2. Resolve to an absolute, canonical path. EvalSymlinks
	// canonicalizes /tmp → /private/tmp on macOS so the comparison
	// with Workspaces() (whose entries are canonical) stays honest.
	// If the target does not exist we still need an absolute path
	// for the ghost lookup — filepath.Abs is the fallback.
	target := resolveRemoveTarget(repo.Root, destination)
	if canon, err := filepath.EvalSymlinks(target); err == nil {
		target = canon
	} else if abs, absErr := filepath.Abs(target); absErr == nil {
		target = abs
	}

	// 3. Current-workspace check: pulling the ground out from under
	// the running process. Both sides go through EvalSymlinks so
	// the /var vs /private/var duality on macOS never masks a match.
	// A Getwd failure is treated as "cwd unknown" — we cannot refuse
	// on grounds we cannot verify, so we let the plan proceed and
	// downstream layers will still catch the same mistake at execute
	// time (git refuses to remove a worktree the caller is inside).
	if cwd, err := os.Getwd(); err == nil {
		if canon, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = canon
		}
		if cwd == target {
			return RemovePlan{}, fmt.Errorf("cannot remove the current workspace: %s", target)
		}
	}

	// 4. Workspace membership. Workspaces()[0] is the primary by
	// convention (git worktree list --porcelain always emits the
	// main worktree first; parseWorktreePorcelain preserves that
	// order). Canonicalize each entry once so the ghost branch and
	// the primary check both compare like-for-like against target.
	workspaces, err := repo.Workspaces()
	if err != nil {
		return RemovePlan{}, err
	}
	canonWorkspaces := make([]string, len(workspaces))
	for i, ws := range workspaces {
		if canon, err := filepath.EvalSymlinks(ws); err == nil {
			canonWorkspaces[i] = canon
		} else {
			canonWorkspaces[i] = ws
		}
	}

	if len(canonWorkspaces) > 0 && canonWorkspaces[0] == target {
		return RemovePlan{}, fmt.Errorf("refusing to remove the primary workspace: %s", target)
	}

	isLive := false
	for _, ws := range canonWorkspaces {
		if ws == target {
			isLive = true
			break
		}
	}

	plan := RemovePlan{
		Target:  target,
		Backend: string(repo.VCS()),
	}
	plan.VCSCommand = renderRemoveCommand(repo.VCS(), target)

	// 5. Ghost vs hard-unknown branch. A registry entry keyed by
	// target proves someone tore the workspace down externally (VCS
	// worktree gone but wrk state still references it), which is
	// exactly what `wrk gc` reconciles. No registry entry means the
	// path was never a workspace of this repo at all.
	if !isLive {
		reg, err := loadRegistry(repo)
		if err != nil {
			return RemovePlan{}, err
		}
		if _, ok := reg[target]; ok {
			plan.IsGhost = true
			plan.DetachedPaths = reg[target]
			plan.Refusal = fmt.Sprintf(
				"%s is not a live workspace; VCS metadata and/or a detach registry entry still reference it. Run 'wrk gc' to sweep the ghost.",
				target,
			)
			return plan, nil
		}
		return RemovePlan{}, fmt.Errorf("%s is not a live workspace of this repo", target)
	}

	// 6. Soft-refusal probes for a live workspace.
	var reasons []string

	// 6a. Uncommitted VCS changes. Only git for now — jj probing
	// belongs to Task 2.2 (backend). A probe failure is silently
	// tolerated: a plan without a refusal we could not detect is
	// still a plan, and the executor will surface real failures.
	if repo.VCS() == repository.Git {
		if count, err := gitUncommittedCount(target); err == nil {
			plan.UncommittedChanges = count
			if count > 0 {
				reasons = append(reasons,
					fmt.Sprintf("workspace has %d uncommitted VCS change(s)", count),
				)
			}
		}
	}

	// 6b. Detached-file registry entries. Removing a workspace
	// whose managed resources have been materialized as independent
	// local copies loses user data — refuse without --force. The
	// exact paths go on the plan so the CLI can render them; the
	// Refusal string names them inline for a single-line summary.
	reg, err := loadRegistry(repo)
	if err != nil {
		return RemovePlan{}, err
	}
	if paths := reg[target]; len(paths) > 0 {
		plan.DetachedPaths = paths
		reasons = append(reasons,
			fmt.Sprintf("workspace has independent local copies: %s",
				strings.Join(paths, ", ")),
		)
	}

	if len(reasons) > 0 {
		plan.Refusal = strings.Join(reasons, "; ")
	}

	return plan, nil
}

// resolveRemoveTarget applies the sibling-default policy shared with
// `wrk new`: absolute paths pass through, bare names become siblings
// of root, other relative paths join against root. It intentionally
// mirrors internal/repository.resolveDestination — the same helper
// but a private, package-scoped duplicate because the original is
// unexported and only relevant to path resolution.
//
// Unlike repository.ResolveDestination, this variant does NOT check
// existence or nesting: `wrk remove` wants the target to exist and
// (for a live workspace) to sit inside a workspace root.
func resolveRemoveTarget(root, destination string) string {
	if filepath.IsAbs(destination) {
		return filepath.Clean(destination)
	}
	if isBareRemoveName(destination) {
		return filepath.Clean(filepath.Join(root, "..", destination))
	}
	return filepath.Clean(filepath.Join(root, destination))
}

// isBareRemoveName reports whether name is a simple identifier — no
// separators, not "." or "..". Matches internal/repository.isBareName
// so `wrk new feature` and `wrk remove feature` resolve identically.
func isBareRemoveName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

// renderRemoveCommand renders the VCS command the executor will run,
// for display in the plan and the confirmation prompt. jj's
// `workspace forget` operates on a NAME not a path, and that name
// lookup lives in the backend layer (Task 2.2), so we leave <name>
// as a placeholder here rather than fabricate a lookup that would
// duplicate backend logic.
func renderRemoveCommand(vcs repository.VCS, target string) string {
	switch vcs {
	case repository.Git:
		return fmt.Sprintf("git worktree remove %s", target)
	case repository.JJ:
		return "jj workspace forget <name>"
	default:
		return fmt.Sprintf("(unknown backend %q)", string(vcs))
	}
}

// gitUncommittedCount runs `git status --porcelain` in target and
// counts the non-empty lines. Each porcelain-v1 line represents one
// changed path (tracked-modified, staged, or untracked), which is
// the granularity the user cares about for a data-loss confirmation.
//
// A probe failure — target missing a `.git`, git binary missing,
// permission denied — returns the underlying error. Callers may
// choose to swallow it (the plan builder does) because a plan
// without an uncommitted-changes signal is still useful; the
// executor sees the same failure at commit time.
func gitUncommittedCount(target string) (int, error) {
	cmd := exec.Command("git", "-C", target, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return 0, nil
	}
	return strings.Count(trimmed, "\n") + 1, nil
}
