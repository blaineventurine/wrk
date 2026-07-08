package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestStaleSymlinkSurvivesHookFailure pins H2: when Link needs to
// rebuild a stale wrk-managed symlink via an initialize hook and the
// hook exits non-zero, the OLD symlink MUST still be on disk pointing
// at the previous (intact) shared target. The historical ordering
// emitted an explicit Remove(workspace) → CreateDirectory →
// InitializeResource → Symlink sequence; a hook failure between steps
// two and four left the workspace with nothing at all AND partial
// shared bytes. The fix reorders so the trailing Symlink action (with
// its own atomic Lstat+Remove) is the only thing that ever touches
// the workspace symlink.
//
// Setup: fingerprint-gated resource where the manifest bump forces a
// new variant, initialize hook is scripted to exit non-zero. Assert
// that after Link fails: (a) the workspace path still exists as a
// symlink, and (b) it still targets the original (v1) shared side, and
// (c) v1 shared bytes are still readable.
func TestStaleSymlinkSurvivesHookFailure(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// The v1 initialize hook succeeds (touch shared marker); the v2
	// hook fails (exit 1). We swap the config text between Link runs
	// to simulate the "hook was fine last time, broken this time"
	// scenario without depending on any shell-side conditional.
	writeConfig(t, repo.Root, config.Filename, staleConfigWithGoodHook())
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}

	// Snapshot the v1 state: what the workspace symlink points at and
	// what's inside v1 shared.
	workspaceLink := filepath.Join(repo.Root, "node_modules")
	v1Target, err := os.Readlink(workspaceLink)
	if err != nil {
		t.Fatalf("readlink v1: %v", err)
	}
	// Prove v1 shared is real, not a red herring — read the marker
	// the hook wrote.
	if _, err := os.Stat(filepath.Join(v1Target, "hook.marker")); err != nil {
		t.Fatalf("v1 hook marker missing before failure: %v", err)
	}

	// Now swap the config to the failing-hook variant AND bump the
	// manifest so the fingerprint changes -> loc.Path moves -> the
	// symlink is stale.
	writeConfig(t, repo.Root, config.Filename, staleConfigWithFailingHook())
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)

	// Link #2 MUST fail — the v2 hook exits non-zero.
	var out bytes.Buffer
	err = Link(repo, Options{StorageRoot: storage, Stdout: &out})
	if err == nil {
		t.Fatalf(
			"Link #2 succeeded despite failing hook; stdout:\n%s",
			out.String(),
		)
	}

	// Post-condition A: the workspace path is STILL a symlink.
	info, err := os.Lstat(workspaceLink)
	if err != nil {
		t.Fatalf("lstat workspace after failed Link #2: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(
			"workspace node_modules is no longer a symlink after "+
				"hook failure — H2 regression; mode=%v",
			info.Mode(),
		)
	}

	// Post-condition B: the symlink still points at v1 target. The
	// Symlink action never ran (hook failed first) so the atomic
	// replace never fired.
	nowTarget, err := os.Readlink(workspaceLink)
	if err != nil {
		t.Fatalf("readlink post-failure: %v", err)
	}
	if nowTarget != v1Target {
		t.Errorf(
			"workspace target after hook failure = %q, want %q "+
				"(the old link must survive so the retry starts from a "+
				"consistent state)",
			nowTarget, v1Target,
		)
	}

	// Post-condition C: v1 shared bytes intact — reading through the
	// preserved symlink resolves the original hook marker file.
	if _, err := os.Stat(filepath.Join(workspaceLink, "hook.marker")); err != nil {
		t.Errorf(
			"reading through preserved symlink failed: %v — v1 shared "+
				"was clobbered by the failed v2 provision",
			err,
		)
	}
}

// staleConfigWithGoodHook is the v1 config: manifest-gated fingerprint,
// initialize hook that succeeds by touching a marker inside {shared}.
func staleConfigWithGoodHook() string {
	return "resources:\n" +
		"  - name: node\n" +
		"    path: node_modules\n" +
		"    fingerprint:\n" +
		"      - \"{root}/manifest.json\"\n" +
		"    hooks:\n" +
		"      initialize:\n" +
		"        - run: sh -c 'mkdir -p {shared} && touch {shared}/hook.marker'\n"
}

// staleConfigWithFailingHook is the v2 config: same fingerprint input,
// but the initialize hook exits non-zero. The Move/Symlink pairing that
// wraps the InitializeResource action is what we're pinning here — if
// the hook fails, the workspace symlink must survive to point at v1.
func staleConfigWithFailingHook() string {
	return "resources:\n" +
		"  - name: node\n" +
		"    path: node_modules\n" +
		"    fingerprint:\n" +
		"      - \"{root}/manifest.json\"\n" +
		"    hooks:\n" +
		"      initialize:\n" +
		"        - run: sh -c 'exit 1'\n"
}

// TestLinkRefusesToReplaceUserSymlinkTargetingOutsideStorage pins H1:
// a workspace symlink the operator manually pointed at an external
// directory MUST NOT be silently replaced by Link. Historically the
// plan built a Remove(workspace) + Symlink(...) pair on the assumption
// that any wrong-target symlink was stale wrk state — but a user link
// expresses intent to track something other than shared storage, and
// clobbering it silently is a data-integrity footgun.
//
// After the fix, Link surfaces a conflict naming the target, and the
// user's symlink is byte-for-byte unchanged on disk.
func TestLinkRefusesToReplaceUserSymlinkTargetingOutsideStorage(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// The user's chosen target: a completely separate directory
	// outside the wrk storage tree. Contents are diagnostic so the
	// post-Link readback proves nothing was replaced.
	userTargetDir := t.TempDir()
	userTargetContent := []byte("user-chose-this-target\n")
	if err := os.WriteFile(filepath.Join(userTargetDir, "marker"), userTargetContent, 0o644); err != nil {
		t.Fatalf("seed user target: %v", err)
	}

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: things\n"+
			"    path: things\n",
	)

	// User manually installs the symlink BEFORE Link ever runs.
	workspacePath := filepath.Join(repo.Root, "things")
	if err := os.Symlink(userTargetDir, workspacePath); err != nil {
		t.Fatalf("prep user symlink: %v", err)
	}

	// Also pre-provision the shared side so buildLink hits the
	// SharedExists=true branch — the historical bug's most damaging
	// path (silent Remove+Symlink over the user's link).
	sharedAbs, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "things"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedAbs, 0o755); err != nil {
		t.Fatalf("prep shared: %v", err)
	}

	var out bytes.Buffer
	err = Link(repo, Options{StorageRoot: storage, Stdout: &out})
	if err == nil {
		t.Fatalf(
			"Link succeeded despite user-managed symlink; stdout:\n%s",
			out.String(),
		)
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error = %q, want to mention conflict", err.Error())
	}
	// The plan output printed to stdout MUST name the user's target
	// so the operator can act without re-inspecting the filesystem.
	if !strings.Contains(out.String(), userTargetDir) {
		t.Errorf(
			"stdout %q does not mention user target %q",
			out.String(), userTargetDir,
		)
	}

	// User symlink UNCHANGED: same kind, same target text.
	info, err := os.Lstat(workspacePath)
	if err != nil {
		t.Fatalf("lstat workspace path: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(
			"workspace path is no longer a symlink after failed Link "+
				"— H1 regression clobbered user's link; mode=%v",
			info.Mode(),
		)
	}
	nowTarget, err := os.Readlink(workspacePath)
	if err != nil {
		t.Fatalf("readlink post-failed-Link: %v", err)
	}
	if nowTarget != userTargetDir {
		t.Errorf(
			"user symlink target changed: got %q, want %q",
			nowTarget, userTargetDir,
		)
	}

	// And the user's payload is still there.
	got, err := os.ReadFile(filepath.Join(workspacePath, "marker"))
	if err != nil {
		t.Fatalf("read through preserved symlink: %v", err)
	}
	if string(got) != string(userTargetContent) {
		t.Errorf(
			"marker content = %q, want %q",
			got, userTargetContent,
		)
	}
}

// TestLinkReplacesWrkManagedStaleSymlink is H1's negative control: a
// stale symlink that IS wrk-written (target sits under our own
// storage tree, just at the wrong fingerprint variant) must still be
// silently repaired. This proves the H1 heuristic doesn't overreach
// and refuse work that's actually safe. Setup mirrors
// TestStaleSymlinkRepairedWhenNewVariantExists but keeps the assertion
// scope local to the H1 axis.
func TestLinkReplacesWrkManagedStaleSymlink(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, staleConfig)
	seedStaleWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}

	// Bump manifest -> fingerprint changes -> symlink is now stale
	// (but still points at a wrk-managed variant path under storage).
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)

	// Pre-provision the new-variant shared side so Link can complete
	// via linkToShared.
	fpV2 := fingerprintFor(t, repo.Root)
	variant2Shared, err := filepath.Abs(
		filepath.Join(storage, repo.RepositoryID, "node_modules", fpV2),
	)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(variant2Shared, "pkg-v2", "index.js"), "v2\n")

	if err := Link(repo, opts); err != nil {
		t.Fatalf(
			"Link #2 failed on wrk-managed stale symlink; H1 heuristic "+
				"is too strict: %v",
			err,
		)
	}

	// Symlink now points at variant-2.
	newTarget, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink post-repair: %v", err)
	}
	if newTarget != variant2Shared {
		t.Errorf(
			"post-repair target = %q, want %q",
			newTarget, variant2Shared,
		)
	}
}

// TestDetachRefusesUserCreatedSymlink pins H4: Detach materializes
// SHARED bytes into the workspace as an independent copy. When the
// workspace symlink was created by the user pointing at an external
// target — not shared storage — copying shared bytes into place
// silently erases the operator's chosen destination. The fix: surface
// a conflict, leave the user's symlink alone.
func TestDetachRefusesUserCreatedSymlink(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// The user's chosen target (external, but with distinctive
	// content so we can prove Detach never wrote over it).
	userTargetDir := t.TempDir()
	userMarker := []byte("user-payload\n")
	if err := os.WriteFile(filepath.Join(userTargetDir, "marker"), userMarker, 0o644); err != nil {
		t.Fatalf("seed user target: %v", err)
	}

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: things\n"+
			"    path: things\n",
	)

	workspacePath := filepath.Join(repo.Root, "things")
	if err := os.Symlink(userTargetDir, workspacePath); err != nil {
		t.Fatalf("prep user symlink: %v", err)
	}

	// Pre-provision shared with DIFFERENT content — proves Detach
	// isn't harmlessly copying identical bytes.
	sharedAbs, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "things"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedAbs, 0o755); err != nil {
		t.Fatalf("prep shared dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharedAbs, "marker"), []byte("shared-bytes\n"), 0o644); err != nil {
		t.Fatalf("prep shared marker: %v", err)
	}

	var out bytes.Buffer
	err = Detach(repo, Options{StorageRoot: storage, Stdout: &out})
	if err == nil {
		t.Fatalf(
			"Detach succeeded despite user-managed symlink; stdout:\n%s",
			out.String(),
		)
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error = %q, want to mention conflict", err.Error())
	}

	// The user's symlink is still on disk pointing at the same
	// external target — Detach must NOT have swapped in shared bytes.
	info, err := os.Lstat(workspacePath)
	if err != nil {
		t.Fatalf("lstat post-failed-Detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(
			"workspace path is no longer a symlink after failed "+
				"Detach — H4 regression converted user's link into "+
				"shared bytes; mode=%v",
			info.Mode(),
		)
	}
	nowTarget, err := os.Readlink(workspacePath)
	if err != nil {
		t.Fatalf("readlink post-failed-Detach: %v", err)
	}
	if nowTarget != userTargetDir {
		t.Errorf(
			"user symlink target changed: got %q, want %q",
			nowTarget, userTargetDir,
		)
	}

	// And reading through the preserved symlink still yields the
	// user's payload, not shared bytes.
	got, err := os.ReadFile(filepath.Join(workspacePath, "marker"))
	if err != nil {
		t.Fatalf("read through preserved symlink: %v", err)
	}
	if string(got) != string(userMarker) {
		t.Errorf(
			"marker content = %q, want %q — Detach clobbered user data",
			got, userMarker,
		)
	}
}
