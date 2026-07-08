package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectCanonicalizesRootThroughSymlink pins B4: Repository.Root
// must be symlink-resolved at detection time so downstream comparisons
// (workspace nesting, current-workspace `*` highlight) work on macOS,
// where /var, /tmp and /var/folders/... are symlinks under /private.
func TestDetectCanonicalizesRootThroughSymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Real repo in a temp dir (which itself may already sit under
	// /private/var/... on macOS — we canonicalize once up-front so
	// the comparison isn't confounded).
	realRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", realRoot, "init").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// A symlink pointing at the real root, in a sibling temp dir so
	// removal is handled by t.TempDir().
	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "link")
	if err := os.Symlink(realRoot, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	repo, err := Detect(linkPath, Auto)
	if err != nil {
		t.Fatalf("Detect via symlink: %v", err)
	}

	// EvalSymlinks(linkPath) is the ground-truth canonical form.
	want, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != want {
		t.Fatalf("Repository.Root = %q, want canonical %q", repo.Root, want)
	}
}

// TestDetectCanonicalizesRootFromNestedPath makes sure detection
// canonicalizes even when the caller passes a path deep inside the
// worktree (the common case: the user's cwd).
func TestDetectCanonicalizesRootFromNestedPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	realRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", realRoot, "init").Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "link")
	if err := os.Symlink(realRoot, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	nested := filepath.Join(linkPath, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	repo, err := Detect(nested, Auto)
	if err != nil {
		t.Fatalf("Detect via nested symlinked path: %v", err)
	}

	want, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root != want {
		t.Fatalf("Repository.Root = %q, want canonical %q", repo.Root, want)
	}
}

// TestFindRootErrorMentionsSearchedPath pins M21: when detection
// bottoms out at the filesystem root without finding a VCS marker,
// the error must name the absolute path we searched from so users can
// tell they were in the wrong directory instead of guessing why wrk
// refused to run.
func TestFindRootErrorMentionsSearchedPath(t *testing.T) {
	// t.TempDir() on macOS/Linux sits under /tmp or /var/folders,
	// far from any user git checkout — findRoot will walk all the
	// way to `/` without finding a marker.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	_, err = findRoot(dir)
	if err == nil {
		t.Fatal("findRoot: expected error outside any repository")
	}

	// The message carries the absolute form of the caller's start
	// path — the pre-EvalSymlinks input, since findRoot uses
	// filepath.Abs. We passed an already-canonical path so both
	// forms match.
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error missing searched path %q: %v", dir, err)
	}
}

// TestDetectVCSGitOnly pins the plain-git branch: a repo whose only
// marker is .git under Auto MUST select Git. This is by far the
// commonest wrk deployment; a swap of the switch arms would route
// every git repo through jj and fail at commonDir.
func TestDetectVCSGitOnly(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".git"))

	got, err := detectVCS(root, Auto)
	if err != nil {
		t.Fatalf("detectVCS(Auto, git-only): %v", err)
	}
	if got != Git {
		t.Fatalf("detectVCS(Auto, git-only) = %q, want %q", got, Git)
	}
}

// TestDetectVCSJJOnly pins the pure-jj branch: `jj init` without
// --colocate leaves only .jj on disk. Under Auto that MUST select JJ.
func TestDetectVCSJJOnly(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".jj"))

	got, err := detectVCS(root, Auto)
	if err != nil {
		t.Fatalf("detectVCS(Auto, jj-only): %v", err)
	}
	if got != JJ {
		t.Fatalf("detectVCS(Auto, jj-only) = %q, want %q", got, JJ)
	}
}

// TestDetectVCSColocatedPrefersJJ pins the documented preference for
// colocated repos: both markers present under Auto MUST resolve to JJ.
// This encodes the design decision that on a colocated checkout the
// user is driving via jj — flipping this silently would break every
// `wrk` invocation on a jj-primary colocated repo.
func TestDetectVCSColocatedPrefersJJ(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".git"))
	makeDir(t, filepath.Join(root, ".jj"))

	got, err := detectVCS(root, Auto)
	if err != nil {
		t.Fatalf("detectVCS(Auto, colocated): %v", err)
	}
	if got != JJ {
		t.Fatalf("detectVCS(Auto, colocated) = %q, want %q (jj preferred)",
			got, JJ)
	}
}

// TestDetectVCSAutoMissingMarkersErrors covers the Auto fall-through:
// findRoot only ascends until it sees a marker, so reaching detectVCS
// with NEITHER marker means the marker vanished between detection and
// selection (a race with `rm -rf .git`, an in-flight VCS migration,
// etc.). detectVCS MUST call out that specific race so the user can
// retry, not conflate it with the generic unsupported-VCS message.
func TestDetectVCSAutoMissingMarkersErrors(t *testing.T) {
	root := t.TempDir()
	// No .git and no .jj on purpose.

	_, err := detectVCS(root, Auto)
	if err == nil {
		t.Fatal("detectVCS(Auto, empty): expected error")
	}
	if !strings.Contains(err.Error(), "vanished") {
		t.Fatalf("error should call out the race, got: %v", err)
	}
}

// TestDetectVCSExplicitGitOnJJErrors pins the explicit-Git guard: if
// the user asked for git but only .jj is present, we MUST NOT
// silently fall back — the caller is asserting a VCS choice and
// wants to know when it does not match reality.
func TestDetectVCSExplicitGitOnJJErrors(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".jj"))

	_, err := detectVCS(root, Git)
	if err == nil {
		t.Fatal("detectVCS(Git, jj-only): expected error")
	}
	if !strings.Contains(err.Error(), "not Git-managed") {
		t.Fatalf("error should say not Git-managed, got: %v", err)
	}
}

// TestDetectVCSExplicitJJOnGitErrors is the mirror of the previous
// test: --vcs=jj on a plain git repo MUST fail loudly.
func TestDetectVCSExplicitJJOnGitErrors(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".git"))

	_, err := detectVCS(root, JJ)
	if err == nil {
		t.Fatal("detectVCS(JJ, git-only): expected error")
	}
	if !strings.Contains(err.Error(), "not Jujutsu-managed") {
		t.Fatalf("error should say not Jujutsu-managed, got: %v", err)
	}
}

// TestDetectVCSExplicitGitOnColocated pins that when the user asks
// for Git explicitly on a colocated repo (both markers), Git is
// respected — the explicit choice overrides the Auto preference for
// jj.
func TestDetectVCSExplicitGitOnColocated(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".git"))
	makeDir(t, filepath.Join(root, ".jj"))

	got, err := detectVCS(root, Git)
	if err != nil {
		t.Fatalf("detectVCS(Git, colocated): %v", err)
	}
	if got != Git {
		t.Fatalf("detectVCS(Git, colocated) = %q, want %q", got, Git)
	}
}

// TestDetectVCSExplicitJJOnColocated is the mirror: --vcs=jj on
// colocated returns JJ. Together with the previous test this pins
// that the explicit arms honor the caller.
func TestDetectVCSExplicitJJOnColocated(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".git"))
	makeDir(t, filepath.Join(root, ".jj"))

	got, err := detectVCS(root, JJ)
	if err != nil {
		t.Fatalf("detectVCS(JJ, colocated): %v", err)
	}
	if got != JJ {
		t.Fatalf("detectVCS(JJ, colocated) = %q, want %q", got, JJ)
	}
}

// TestDetectVCSUnknownPreferredErrors pins the default arm: an
// unrecognized VCS string yields "unsupported VCS" rather than
// crashing at backendFor. ParseVCS should catch this earlier, but
// detectVCS is called through internal paths too — the guard here
// keeps the layering honest.
func TestDetectVCSUnknownPreferredErrors(t *testing.T) {
	root := t.TempDir()
	makeDir(t, filepath.Join(root, ".git"))

	_, err := detectVCS(root, VCS("hg"))
	if err == nil {
		t.Fatal("detectVCS(unknown): expected error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error should say unsupported, got: %v", err)
	}
}

// TestDetectOutsideAnyRepoErrors pins that Detect propagates
// findRoot's failure when called from a path that isn't inside any
// git or jj repository. Silently returning a non-nil *Repository
// with a zero root would set the caller up to write metadata into
// wildly wrong places.
func TestDetectOutsideAnyRepoErrors(t *testing.T) {
	dir := canonPath(t, t.TempDir())

	repo, err := Detect(dir, Auto)
	if err == nil {
		t.Fatalf("Detect outside any repo: got %v, want error", repo)
	}
	if repo != nil {
		t.Fatalf("Detect returned non-nil repo %v on error", repo)
	}
	// findRoot's error names the searched path — this is the
	// contract propagated up through Detect.
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("error missing searched path %q: %v", dir, err)
	}
}

// TestDetectPropagatesCommonDirError pins that a broken colocation
// (jj repo whose .git was removed out-of-band) surfaces as an error
// from Detect, not a partially-populated *Repository. A caller that
// assumed Detect either failed or returned a working repository
// would otherwise dereference a bogus metadataDir.
func TestDetectPropagatesCommonDirError(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	root := canonPath(t, t.TempDir())
	initColocatedJJRepo(t, root)
	// Remove .git so the colocated pairing is broken but .jj
	// remains — detectVCS still picks JJ, commonDir then fails.
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("rm .git: %v", err)
	}

	repo, err := Detect(root, Auto)
	if err == nil {
		t.Fatalf("Detect on broken colocated: got %v, want error", repo)
	}
	if repo != nil {
		t.Fatalf("Detect returned non-nil repo %v on error", repo)
	}
	// The wrap put in by jjBackend.commonDir names "colocated" —
	// pinning that phrase confirms Detect passed the wrap through
	// verbatim instead of re-wrapping / dropping the guidance.
	if !strings.Contains(err.Error(), "colocated") {
		t.Fatalf("error missing colocation guidance: %v", err)
	}
}
