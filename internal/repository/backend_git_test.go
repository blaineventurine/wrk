package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestParseWorktreePorcelainFiltersBareAndPrunable pins D6: the
// porcelain parser must return only live worktrees. Bare-primary
// records (no working tree, sometimes no `worktree` line at all) and
// prunable records (broken link that would fail Detect if walked)
// were previously indistinguishable from live ones because we grepped
// for `worktree <path>` lines only.
func TestParseWorktreePorcelainFiltersBareAndPrunable(t *testing.T) {
	// Three-record output. Live first (standard record with HEAD +
	// branch), prunable second (has a worktree line but is tagged
	// broken), bare third (no worktree line — the shape emitted for
	// bare-primary repos).
	//
	// Format follows git-worktree(1) --porcelain: records separated
	// by blank lines, "key value" lines, key-only sentinels.
	sample := "worktree /path/to/live\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef01\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /path/to/prunable\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef02\n" +
		"prunable gitdir file points to non-existent location\n" +
		"\n" +
		"bare\n"

	got := parseWorktreePorcelain(sample)
	want := []string{"/path/to/live"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain returned %v, want %v", got, want)
	}
}

// TestParseWorktreePorcelainBareWithWorktreeLineFiltered guards the
// other bare-primary shape: some git versions emit
// `worktree <path>\nbare` for the bare record. The `bare` sentinel
// must drop the whole record even when a `worktree` line is present.
func TestParseWorktreePorcelainBareWithWorktreeLineFiltered(t *testing.T) {
	sample := "worktree /path/to/bare-primary.git\n" +
		"bare\n" +
		"\n" +
		"worktree /path/to/live\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef01\n" +
		"branch refs/heads/main\n"

	got := parseWorktreePorcelain(sample)
	want := []string{"/path/to/live"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain returned %v, want %v", got, want)
	}
}

// TestParseWorktreePorcelainBareOnly returns an empty slice — a bare
// primary with no worktrees is a legitimate output and must not
// synthesize a fake path.
func TestParseWorktreePorcelainBareOnly(t *testing.T) {
	sample := "worktree /path/to/bare.git\n" +
		"bare\n"

	got := parseWorktreePorcelain(sample)
	if len(got) != 0 {
		t.Fatalf("parseWorktreePorcelain returned %v, want empty", got)
	}
}

// TestParseWorktreePorcelainMultipleLive keeps the happy path honest:
// several live worktrees, all returned, in order.
func TestParseWorktreePorcelainMultipleLive(t *testing.T) {
	sample := "worktree /a\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef01\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /b\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef02\n" +
		"branch refs/heads/feature\n" +
		"\n" +
		"worktree /c\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef03\n" +
		"detached\n"

	got := parseWorktreePorcelain(sample)
	want := []string{"/a", "/b", "/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain returned %v, want %v", got, want)
	}
}

// TestGitBackendKind pins the trivial dispatcher — a swap of the two
// backends would silently route git repos through jjBackend, which
// would then produce baffling `jj git root` errors on a plain git
// repo.
func TestGitBackendKind(t *testing.T) {
	if got := (gitBackend{}).kind(); got != Git {
		t.Fatalf("gitBackend.kind() = %q, want %q", got, Git)
	}
}

// TestGitBackendCreateWorkspace exercises the real `git worktree add`
// path: a repository with an initial commit, plus a fresh sibling
// destination. Success means the destination is a directory whose
// `.git` is the file (not directory) that identifies a LINKED
// worktree — the shape wrk relies on for every downstream Detect.
func TestGitBackendCreateWorkspace(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	dest := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, dest, ""); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat destination: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("destination %q is not a directory", dest)
	}

	// Linked worktrees identify themselves with a FILE `.git`
	// containing `gitdir: <path>`, not a `.git` directory. That
	// distinction is what lets `git rev-parse --git-common-dir`
	// resolve back to the primary's metadata. If a swap of `git
	// worktree add` for `git init` ever crept in, .git would be a
	// dir and this would catch it.
	dotGit := filepath.Join(dest, ".git")
	gitInfo, err := os.Stat(dotGit)
	if err != nil {
		t.Fatalf("stat .git: %v", err)
	}
	if gitInfo.IsDir() {
		t.Fatalf(".git in linked worktree is a directory; want a gitdir file")
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		t.Fatalf("read .git file: %v", err)
	}
	if !strings.HasPrefix(string(data), "gitdir:") {
		t.Fatalf(".git file must start with `gitdir:`; got %q", string(data))
	}
}

// TestGitBackendCreateWorkspaceWithBase exercises the `--base <ref>`
// path: given a repository with an extra pre-existing branch, the
// backend forks a fresh branch off <base> named after the destination
// basename. Success means the destination is a linked worktree AND
// its HEAD sits on a branch called after the last path component —
// which is git's default `-b` shape when no branch name is given.
// This test would fail if the backend accidentally checked out
// <base> directly (branch would equal `feature-base`, sharing a
// checkout that git refuses on the next worktree add) or dropped the
// `-b` altogether (branch would equal `HEAD` on a detached checkout).
func TestGitBackendCreateWorkspaceWithBase(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	// Pre-existing branch to fork off of.
	branchCmd := exec.Command("git", "branch", "feature-base")
	branchCmd.Dir = root
	if out, err := branchCmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch feature-base: %v\n%s", err, out)
	}

	dest := filepath.Join(parent, "secondary")
	if err := (gitBackend{}).createWorkspace(root, dest, "feature-base"); err != nil {
		t.Fatalf("createWorkspace with base: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest not created: %v", err)
	}

	// The new branch is named after the destination basename — the
	// contract that keeps subsequent `wrk new` calls off the same
	// <base> from colliding on branch names.
	got, err := capture(dest, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD in secondary: %v", err)
	}
	if strings.TrimSpace(got) != "secondary" {
		t.Errorf("HEAD branch = %q, want %q", strings.TrimSpace(got), "secondary")
	}
}

// TestGitBackendCreateWorkspaceWithBaseErrorsOnBranchCollision pins
// the wrk-level preflight for a branch-name collision. Without the
// preflight, `git worktree add -b <existing>` returns git's raw
// `fatal: a branch named 'existing' already exists.` — accurate but
// unhelpful to a user staring at a failed `wrk new`. The preflight
// surfaces a wrk-shaped error naming the branch and the remediation,
// so the CLI's exit message points the user at the fix.
//
// The success path stays unchanged (see the sibling with-base test)
// — only the collision path adds a check.
func TestGitBackendCreateWorkspaceWithBaseErrorsOnBranchCollision(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	// Pre-create the branch that would collide with the destination
	// basename. `secondary` matches the last segment of dest below.
	branchCmd := exec.Command("git", "branch", "secondary")
	branchCmd.Dir = root
	if out, err := branchCmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch secondary: %v\n%s", err, out)
	}

	dest := filepath.Join(parent, "secondary")
	err := (gitBackend{}).createWorkspace(root, dest, "HEAD")
	if err == nil {
		t.Fatal("expected error for existing branch name; got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists' so users can grep for it; got: %v", err)
	}
	if !strings.Contains(err.Error(), "secondary") {
		t.Errorf("error should name the branch %q so the remediation makes sense; got: %v", "secondary", err)
	}
	// Preflight MUST fail BEFORE `git worktree add` runs — otherwise
	// git would leave partial metadata behind that `wrk gc` would
	// have to clean up. The absence of the destination directory is
	// the strongest proof that nothing ran.
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("destination %q should not exist after preflight refusal; stat err = %v", dest, statErr)
	}
}

// TestGitBackendCreateWorkspaceEmptyBasePreservesLegacyBehavior pins
// the base="" contract: the backend MUST behave exactly like the
// pre-`--base` code path (`git worktree add -- <dest>`), so callers
// that never set --base see zero behaviour change. Signal: the
// destination checks out fine AND its HEAD branch matches the
// primary's HEAD branch — which git derives from the last path
// component absent an explicit `-b`.
func TestGitBackendCreateWorkspaceEmptyBasePreservesLegacyBehavior(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	dest := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, dest, ""); err != nil {
		t.Fatalf("createWorkspace empty base: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest not created: %v", err)
	}
	// Legacy `git worktree add` derives the branch name from the
	// last path component. If the empty-base branch ever routed
	// through `-b <basename> <ref>` unintentionally we would still
	// land on this same name; the sibling `WithBase` test above
	// covers the intentional case.
	got, err := capture(dest, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if strings.TrimSpace(got) != "feature" {
		t.Errorf("HEAD branch = %q, want %q", strings.TrimSpace(got), "feature")
	}
}

// TestGitBackendWorkspacesListsAll seeds primary + secondary worktrees
// and asserts workspaces() returns BOTH canonical roots, primary
// first. The order matters for callers like `wrk list` that print the
// primary as the anchor of the repository.
func TestGitBackendWorkspacesListsAll(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, secondary, ""); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}

	got, err := (gitBackend{}).workspaces(root)
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	want := []string{root, secondary}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaces(%q) = %v, want %v (primary first)",
			root, got, want)
	}
}

// TestGitBackendWorkspacesSkipsPrunable pins D6 end-to-end: when a
// secondary worktree directory is deleted out-of-band (`rm -rf`
// instead of `git worktree remove`), `git worktree list --porcelain`
// still emits a record for it but tags it `prunable`. workspaces()
// MUST drop that record — anything downstream would try to walk into
// a directory that no longer exists.
func TestGitBackendWorkspacesSkipsPrunable(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, secondary, ""); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}

	// Delete the secondary directory the wrong way — this is what
	// users do when they forget `git worktree remove` and rm/mv the
	// directory manually. git will mark it prunable on next list.
	if err := os.RemoveAll(secondary); err != nil {
		t.Fatalf("rm secondary: %v", err)
	}

	got, err := (gitBackend{}).workspaces(root)
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	want := []string{root}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspaces(%q) = %v, want %v (prunable dropped)",
			root, got, want)
	}
}

// TestGitBackendWorkspacesHandlesBareRecord is the other half of D6:
// a bare primary emits a record whose sole payload is the `bare`
// sentinel (no working tree). `git clone --bare` produces exactly
// that shape, and workspaces() MUST NOT synthesize a fake path from
// it — the parser was previously grepping for `worktree <path>` and
// happily returned a bare .git URL as if it were a worktree.
func TestGitBackendWorkspacesHandlesBareRecord(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	source := filepath.Join(parent, "source")
	makeDir(t, source)
	initGitRepo(t, source)

	// Bare clone of the seed repo. `git worktree list` in a bare
	// repo emits the `bare` sentinel; there is no primary working
	// tree to walk. We add one secondary worktree so the output
	// contains a bare record AND a live record — proving the parser
	// filters the former and keeps the latter, not the "no records
	// at all" trivial pass.
	bareRepo := filepath.Join(parent, "bare.git")
	cmd := exec.Command("git", "clone", "--bare", "--quiet", source, bareRepo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}

	secondary := filepath.Join(parent, "linked")
	if err := (gitBackend{}).createWorkspace(bareRepo, secondary, ""); err != nil {
		t.Fatalf("createWorkspace from bare: %v", err)
	}

	got, err := (gitBackend{}).workspaces(bareRepo)
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	// The bare primary contributes nothing — only the linked
	// worktree is a real workspace.
	if len(got) != 1 || got[0] != secondary {
		t.Fatalf("workspaces from bare repo = %v, want [%q] (bare record dropped)",
			got, secondary)
	}
	if slices.Contains(got, bareRepo) {
		t.Fatalf("workspaces returned the bare .git path %q; parser must drop `bare` records",
			bareRepo)
	}
}

// TestGitBackendCommonDirFromWorktree pins that commonDir returns the
// PRIMARY's metadata dir even when invoked from a LINKED worktree —
// wrk uses this for identity hashing and the detach registry, both of
// which MUST resolve to the same path from every workspace.
func TestGitBackendCommonDirFromWorktree(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, secondary, ""); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}

	primaryDir, err := (gitBackend{}).commonDir(root)
	if err != nil {
		t.Fatalf("commonDir(primary): %v", err)
	}
	linkedDir, err := (gitBackend{}).commonDir(secondary)
	if err != nil {
		t.Fatalf("commonDir(linked): %v", err)
	}

	// Canonicalize both — git may return a symlinked form.
	if canonPath(t, primaryDir) != canonPath(t, linkedDir) {
		t.Fatalf("commonDir mismatch between primary and linked worktree:\n primary=%q\n linked=%q",
			primaryDir, linkedDir)
	}
	// And the answer MUST be the primary's .git, not the linked
	// worktree's own gitdir file location.
	wantSuffix := filepath.Join("main", ".git")
	if !strings.HasSuffix(canonPath(t, primaryDir), wantSuffix) {
		t.Fatalf("commonDir(primary) = %q, want suffix %q",
			primaryDir, wantSuffix)
	}
}

// TestGitBackendWorkspacesErrorsOutsideRepo pins the failure path of
// `git worktree list`: pointing at a directory that isn't a git repo
// MUST surface an error, not silently return an empty slice. A
// caller that treated no-error+empty as "clean primary" would happily
// wipe workspace state in a directory that isn't wrk's business.
func TestGitBackendWorkspacesErrorsOutsideRepo(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	got, err := (gitBackend{}).workspaces(t.TempDir())
	if err == nil {
		t.Fatalf("workspaces on non-repo: got %v, want error", got)
	}
	if got != nil {
		t.Fatalf("workspaces error path returned %v, want nil slice",
			got)
	}
}

// TestGitBackendCreateWorkspaceErrorsOutsideRepo covers the passthrough
// failure branch: `git worktree add` refuses to run outside a git
// repo. The error is what stops CreateWorkspace from proceeding to
// Detect on a directory the backend never actually populated.
func TestGitBackendCreateWorkspaceErrorsOutsideRepo(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	root := canonPath(t, t.TempDir())
	dest := filepath.Join(filepath.Dir(root), "would-be-worktree")

	err := (gitBackend{}).createWorkspace(root, dest, "")
	if err == nil {
		t.Fatal("createWorkspace outside repo: expected error")
	}
	// The wrap MUST name the command so the log-reading user can
	// tell which invocation failed.
	if !strings.Contains(err.Error(), "git") {
		t.Fatalf("error missing command name: %v", err)
	}
	// And it MUST NOT have created the destination — a partial init
	// would fool a subsequent CreateWorkspace into "already exists".
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest should not exist after failed createWorkspace; stat err = %v",
			statErr)
	}
}

// TestGitBackendCommonDirErrorsOutsideRepo pins that commonDir
// surfaces the underlying `git rev-parse` failure when called
// outside any repository — the contract callers of commonDir rely on
// to distinguish "no repo here" from a valid path they should treat
// as metadataDir. A silent empty string would lead the caller to
// write the detach registry into an arbitrary directory.
func TestGitBackendCommonDirErrorsOutsideRepo(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	got, err := (gitBackend{}).commonDir(t.TempDir())
	if err == nil {
		t.Fatalf("commonDir outside repo: got %q, want error", got)
	}
	if got != "" {
		t.Fatalf("commonDir error path returned %q, want empty", got)
	}
}

// TestGitBackendDetectGhostsFindsRemoved seeds a secondary worktree,
// rm -rf's it out-of-band (the way users do when they forget
// `git worktree remove`), and asserts detectGhosts returns exactly
// the missing worktree's canonical root. This is the sibling of
// TestGitBackendWorkspacesSkipsPrunable: workspaces() drops prunable
// records so callers don't walk into missing dirs; detectGhosts()
// returns them so `wrk gc` can reconcile the metadata.
func TestGitBackendDetectGhostsFindsRemoved(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, secondary, ""); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}
	if err := os.RemoveAll(secondary); err != nil {
		t.Fatalf("rm secondary: %v", err)
	}

	got, err := (gitBackend{}).detectGhosts(root)
	if err != nil {
		t.Fatalf("detectGhosts: %v", err)
	}

	want := []string{secondary}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detectGhosts(%q) = %v, want %v",
			root, got, want)
	}
}

// TestGitBackendDetectGhostsEmptyWhenClean pins the empty case: a
// clean repository with a single live primary returns an empty
// (non-nil) slice. `wrk gc` treats a nil return as "backend
// failed" — the empty slice is the contract for "nothing to prune".
func TestGitBackendDetectGhostsEmptyWhenClean(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	root := canonPath(t, t.TempDir())
	initGitRepo(t, root)

	got, err := (gitBackend{}).detectGhosts(root)
	if err != nil {
		t.Fatalf("detectGhosts: %v", err)
	}

	if got == nil {
		t.Fatalf("detectGhosts(clean) = nil, want []string{}")
	}
	if len(got) != 0 {
		t.Fatalf("detectGhosts(clean) = %v, want empty", got)
	}
}

// TestGitBackendPruneGhostsClearsMetadata seeds a ghost worktree,
// prunes it, and checks BOTH invariants: the returned slice names
// the pruned path, AND a follow-up `git worktree list --porcelain`
// no longer emits any record for it. Just returning the path without
// running the underlying `git worktree prune` would leave metadata
// unreconciled and the next `wrk` invocation would surface the same
// ghost.
func TestGitBackendPruneGhostsClearsMetadata(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	secondary := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, secondary, ""); err != nil {
		t.Fatalf("createWorkspace secondary: %v", err)
	}
	if err := os.RemoveAll(secondary); err != nil {
		t.Fatalf("rm secondary: %v", err)
	}

	pruned, err := (gitBackend{}).pruneGhosts(root)
	if err != nil {
		t.Fatalf("pruneGhosts: %v", err)
	}

	want := []string{secondary}
	if !reflect.DeepEqual(pruned, want) {
		t.Fatalf("pruneGhosts(%q) returned %v, want %v",
			root, pruned, want)
	}

	// After prune, the metadata must be clean — no record for the
	// dead worktree in the porcelain listing.
	out, err := capture(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("post-prune worktree list: %v", err)
	}
	if strings.Contains(out, "feature") {
		t.Errorf("expected no `feature` entry after prune; got:\n%s", out)
	}

	// A second prune on the same clean repo must return the empty
	// slice — the operation is idempotent from the caller's view.
	pruned, err = (gitBackend{}).pruneGhosts(root)
	if err != nil {
		t.Fatalf("second pruneGhosts: %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("second pruneGhosts returned %v, want empty", pruned)
	}
}

// TestGitBackendDetectGhostsErrorsOutsideRepo mirrors
// TestGitBackendWorkspacesErrorsOutsideRepo: pointing detectGhosts at
// a directory that isn't a git repo MUST surface an error, not
// silently return an empty slice. A caller treating no-error+empty
// as "clean" would happily report success for a directory git never
// looked at.
func TestGitBackendDetectGhostsErrorsOutsideRepo(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	got, err := (gitBackend{}).detectGhosts(t.TempDir())
	if err == nil {
		t.Fatalf("detectGhosts outside repo: got %v, want error", got)
	}
	if got != nil {
		t.Fatalf("detectGhosts error path returned %v, want nil", got)
	}
}

// TestParsePrunableWorktreesFiltersLiveAndBare pins the pure parser:
// the mirror of parseWorktreePorcelain must return ONLY prunable
// records, dropping live worktrees and bare-primary records. A
// swap of the two filter conditions would send `wrk gc` after live
// worktrees.
func TestParsePrunableWorktreesFiltersLiveAndBare(t *testing.T) {
	// Two live records, one bare, one prunable — only the prunable
	// one may appear in the return.
	input := strings.Join([]string{
		"worktree /repo/main",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /repo/live",
		"HEAD def456",
		"branch refs/heads/live",
		"",
		"worktree /repo/bare.git",
		"bare",
		"",
		"worktree /repo/gone",
		"HEAD 000000",
		"prunable gitdir file points to non-existent location",
		"",
	}, "\n")

	got := parsePrunableWorktrees(input)
	want := []string{"/repo/gone"}
	if !slices.Equal(got, want) {
		t.Fatalf("parsePrunableWorktrees = %v, want %v", got, want)
	}
}

// TestParsePrunableWorktreesEmptyOnClean guards the empty return: no
// prunable record ⇒ empty (non-nil) slice, so the backend contract
// holds without a nil-check at every caller.
func TestParsePrunableWorktreesEmptyOnClean(t *testing.T) {
	input := strings.Join([]string{
		"worktree /repo/main",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
	}, "\n")

	got := parsePrunableWorktrees(input)
	if got == nil {
		t.Fatalf("parsePrunableWorktrees(clean) = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("parsePrunableWorktrees(clean) = %v, want empty", got)
	}
}

// TestGitBackendRemoveWorkspace exercises the happy path: seed a
// linked worktree, then removeWorkspace(force=false) MUST both
// delete the directory AND drop the metadata record so a follow-up
// porcelain listing is clean. Just running `git worktree remove`
// with a wrong path (or forgetting to pass `--`) would leave the
// worktree standing and the assertion below would catch it.
func TestGitBackendRemoveWorkspace(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	feature := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, feature, ""); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}

	if err := (gitBackend{}).removeWorkspace(root, feature, false, nil); err != nil {
		t.Fatalf("removeWorkspace: %v", err)
	}

	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("worktree directory survives removeWorkspace: err=%v", err)
	}

	// The porcelain listing must no longer mention the removed
	// worktree. A remove that silently no-op'd would still show the
	// record and let a downstream Detect stumble into a stale path.
	out, err := capture(root, "git", "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("post-remove worktree list: %v", err)
	}
	if strings.Contains(out, "feature") {
		t.Errorf("post-remove porcelain still mentions feature:\n%s", out)
	}
}

// TestGitBackendRemoveWorkspaceIdempotent pins the "already gone"
// branch: a target path that git never registered as a worktree
// MUST NOT surface an error — the executor calls this defensively
// so a user who rm -rf'd their worktree manually can still run the
// `wrk` remove command without hitting a red herring.
func TestGitBackendRemoveWorkspaceIdempotent(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	nonexistent := filepath.Join(parent, "never-was")
	if err := (gitBackend{}).removeWorkspace(root, nonexistent, false, nil); err != nil {
		t.Errorf("idempotent removeWorkspace of missing target: %v", err)
	}
}

// TestGitBackendRemoveWorkspaceForce covers the --force branch: git
// refuses to remove a worktree whose working tree contains
// modifications or untracked files, so the force=true argument MUST
// reach the git CLI as `--force`. A regression that dropped the
// flag (or applied it in the wrong order relative to `--`) would
// leave the worktree standing and the outer test would fail.
func TestGitBackendRemoveWorkspaceForce(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	feature := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, feature, ""); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}

	// Untracked file inside the worktree — enough for `git worktree
	// remove` (without --force) to refuse, so force=false would
	// fail this test and prove the flag is what carried the day.
	if err := os.WriteFile(
		filepath.Join(feature, "dirty"),
		[]byte("unclean"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := (gitBackend{}).removeWorkspace(root, feature, true, nil); err != nil {
		t.Fatalf("removeWorkspace --force: %v", err)
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("force remove left dir behind: err=%v", err)
	}
}

// TestRepositoryRemoveWorkspace pins the Repository-level wrapper:
// Detect + RemoveWorkspace on a live secondary worktree MUST drop
// the worktree, exercising Repository.RemoveWorkspace's filepath.Abs
// canonicalization plus dispatch through the backend interface.
func TestRepositoryRemoveWorkspace(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	parent := canonPath(t, t.TempDir())
	root := filepath.Join(parent, "main")
	makeDir(t, root)
	initGitRepo(t, root)

	feature := filepath.Join(parent, "feature")
	if err := (gitBackend{}).createWorkspace(root, feature, ""); err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}

	repo, err := Detect(root, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if err := repo.RemoveWorkspace(feature, false, nil); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("worktree directory survives RemoveWorkspace: err=%v", err)
	}
}

// TestGitBackendUncommittedCountClean pins that a fresh worktree
// with no working-copy changes reports zero. A probe failure at
// this stage would surface the underlying error rather than a bogus
// count of 0.
func TestGitBackendUncommittedCountClean(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	root := canonPath(t, t.TempDir())
	initGitRepo(t, root)

	count, err := (gitBackend{}).uncommittedCount(root)
	if err != nil {
		t.Fatalf("uncommittedCount: %v", err)
	}
	if count != 0 {
		t.Errorf("clean worktree count = %d, want 0", count)
	}
}

// TestGitBackendUncommittedCountDirty pins that a workspace with a
// single untracked file reports exactly 1. The exact value matters:
// the plan builder propagates it verbatim into the refusal message.
func TestGitBackendUncommittedCountDirty(t *testing.T) {
	skipIfNoGit(t)
	isolateGitConfig(t)

	root := canonPath(t, t.TempDir())
	initGitRepo(t, root)

	if err := os.WriteFile(
		filepath.Join(root, "untracked.txt"),
		[]byte("dirt"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	count, err := (gitBackend{}).uncommittedCount(root)
	if err != nil {
		t.Fatalf("uncommittedCount: %v", err)
	}
	if count != 1 {
		t.Errorf("dirty worktree count = %d, want 1", count)
	}
}

// TestGitBackendUncommittedCountProbeFailure pins that pointing the
// probe at a directory with no `.git` surfaces the underlying error
// instead of silently reporting 0 — otherwise a probe failure would
// be indistinguishable from a clean worktree and the plan builder
// would suppress a refusal it should surface.
func TestGitBackendUncommittedCountProbeFailure(t *testing.T) {
	skipIfNoGit(t)

	_, err := (gitBackend{}).uncommittedCount(t.TempDir())
	if err == nil {
		t.Fatal("uncommittedCount on non-git dir: want error, got nil")
	}
}
