package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blaineventurine/wrk/internal/repository"
)

// RemovePlan describes the actions `wrk remove` would take. It is
// the read-only output of BuildRemovePlan: no mutation has occurred
// yet. The CLI (Task 2.4) prints it and the executor (Task 2.3)
// applies it.
//
// Refusal is the composed, human-readable reason a soft refusal
// applies (uncommitted changes, detached files, and/or isolated
// variants). Multiple reasons are joined by "; " so a single string
// suffices for the CLI's --force decision.
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

	// IsolatedPaths is the workspace-relative resource paths the
	// isolation registry records for Target. Removing the workspace
	// orphans these entries: the variants' content — by definition
	// not reproducible by hooks — becomes unreferenced and the next
	// `wrk gc` sweeps it. Empty when the workspace has no isolation
	// entries.
	IsolatedPaths []string

	// IsGhost is true when Target is NOT in Workspaces() but the
	// detach registry still carries an entry keyed by it — a stale
	// bookkeeping artefact `wrk gc` should sweep.
	IsGhost bool
	// Refusal is the composed refusal reason. Empty when no refusal
	// applies. Ghost cases populate Refusal with a hint at `wrk gc`
	// (and the caller MUST branch on IsGhost, not Refusal-emptiness,
	// to distinguish soft from ghost refusals).
	Refusal string

	// TotalBytes is the sum of regular-file bytes under Target,
	// computed by treeSize during BuildRemovePlan. Best-effort: walk
	// errors are swallowed so a partial reading yields a lower bound
	// rather than aborting the plan (same policy as `wrk list --size`
	// and BuildForgetPlan). The CLI uses this to size the progress
	// bar; 0 means either the target has no regular files or the
	// walk failed early.
	TotalBytes int64
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
//  7. Isolation registry entries      → soft refusal, --force may
//     override.
//
// Cases 5–7 can coexist; the reasons accumulate into Refusal
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
			return RemovePlan{}, Newf(ErrCurrentWorkspace,
				"cd elsewhere before running 'wrk remove'",
				"cannot remove the current workspace: %s", target)
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
		return RemovePlan{}, Newf(ErrPrimaryWorkspace,
			"remove secondary workspaces individually; use 'wrk forget' to drop the entire repo",
			"refusing to remove the primary workspace: %s", target)
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

	// TotalBytes is populated for every code path that returns a
	// plan (live workspace or ghost). treeSize tolerates partial
	// walk failures internally; the error is swallowed here so a
	// filesystem hiccup does not abort the plan — a slightly-low
	// size is preferable to no plan at all, matching the policy
	// shared with BuildForgetPlan and `wrk list --size`.
	plan.TotalBytes, _ = treeSize(target)

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
		hint := "run 'wrk workspaces' to list live workspaces"
		if _, statErr := os.Stat(target); statErr == nil {
			// The directory is on disk but neither the VCS nor the
			// registry knows it — the fingerprint of an interrupted
			// removal (VCS metadata dropped, directory sweep never
			// finished). Route the user at the leftover instead of a
			// generic listing hint.
			hint = "the directory exists on disk but is not a registered workspace — " +
				"a previous removal may have been interrupted; remove it manually if it is a leftover"
		}
		return RemovePlan{}, Newf(ErrNotLiveWorkspace, hint,
			"%s is not a live workspace of this repo", target)
	}

	// 6. Soft-refusal probes for a live workspace.
	var reasons []string

	// 6a. Uncommitted VCS changes. Backend-agnostic: the repository
	// package dispatches to git (status --porcelain) or jj (diff
	// --summary). A probe failure is silently tolerated — a plan
	// without a refusal we could not detect is still a plan, and the
	// executor will surface real failures.
	if count, err := repo.UncommittedCount(target); err == nil {
		plan.UncommittedChanges = count
		if count > 0 {
			reasons = append(reasons,
				fmt.Sprintf("workspace has %d uncommitted VCS change(s)", count),
			)
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

	// 6c. Isolated variants. Removing the workspace orphans its
	// isolation entries; the variants' content — by definition not
	// reproducible by hooks — becomes unreferenced and the next
	// `wrk gc` sweeps it. The user must know BEFORE the workspace
	// vanishes.
	iso, err := loadIsolation(repo)
	if err != nil {
		return RemovePlan{}, err
	}
	if entries := iso[target]; len(entries) > 0 {
		paths := make([]string, 0, len(entries))
		for resourcePath := range entries {
			paths = append(paths, resourcePath)
		}
		sort.Strings(paths)
		plan.IsolatedPaths = paths
		reasons = append(reasons, fmt.Sprintf(
			"workspace has isolated variant(s) whose content will become unreferenced and swept by `wrk gc`: %s",
			strings.Join(paths, ", ")))
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
// lookup lives in the backend layer, so we leave <name> as a
// placeholder here; the backend also removes the working-copy
// directory after forget succeeds, which the rendered command
// echoes with a trailing `rm -rf`.
func renderRemoveCommand(vcs repository.VCS, target string) string {
	switch vcs {
	case repository.Git:
		return fmt.Sprintf("git worktree remove %s", target)
	case repository.JJ:
		return fmt.Sprintf("jj workspace forget <name>; rm -rf %s", target)
	default:
		return fmt.Sprintf("(unknown backend %q)", string(vcs))
	}
}

// ExecuteRemove tears down plan.Target: runs the VCS remove command
// (idempotent per backend contract) then clears any detach-registry
// entry keyed by the target and the isolation entries snapshotted on
// plan.IsolatedPaths. Callers must have already applied the safety
// gates from BuildRemovePlan / Confirm.
//
// options.Progress, if non-nil, is fired for each regular file
// removed by the wrk-side directory sweep. Only the jj backend
// fires it: `git worktree remove` runs its own subprocess and we
// cannot inspect its per-file deletes. See
// backend.removeWorkspace's contract for the asymmetry.
func ExecuteRemove(repo *repository.Repository, plan RemovePlan, force bool, options Options) error {
	if err := repo.RemoveWorkspace(plan.Target, force, options.Progress); err != nil {
		return err
	}
	if err := withRegistryLock(repo, func() error {
		reg, err := loadRegistry(repo)
		if err != nil {
			return err
		}
		if _, ok := reg[plan.Target]; !ok {
			return nil
		}
		delete(reg, plan.Target)
		return saveRegistry(repo, reg)
	}); err != nil {
		return err
	}
	// Clear the isolation entries only AFTER the workspace removal
	// succeeded — on failure the workspace still exists and the
	// entries must survive. Clearing here keeps the registry honest
	// immediately instead of waiting for the next gc's orphan sweep.
	for _, resourcePath := range plan.IsolatedPaths {
		if err := clearIsolation(repo, plan.Target, resourcePath); err != nil {
			return err
		}
	}
	return nil
}
