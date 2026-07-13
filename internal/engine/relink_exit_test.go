package engine

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/planner"
	"github.com/blaineventurine/wrk/internal/repository"
)

// isolatedNodeFixture is a workspace whose fingerprinted node_modules
// went through the real Link → Detach → RelinkIsolate flow (reusing
// the relink_isolate_test.go fixture pieces): the workspace symlink
// points at an isolated-<hex> variant, the fingerprint variant with
// the original shared bytes ("v1\n" marker) still sits beside it in
// storage, and the isolation registry records the pin.
type isolatedNodeFixture struct {
	repo *repository.Repository
	opts Options

	// wsPath is the workspace-side node_modules path.
	wsPath string
	// sharedTarget is the fingerprint-variant path Link originally
	// pinned — where relink must repoint the symlink.
	sharedTarget string
	// entry is the isolation-registry record for the isolated variant.
	entry isolationEntry
}

func newIsolatedNodeFixture(t *testing.T) isolatedNodeFixture {
	t.Helper()

	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	wsPath := filepath.Join(repo.Root, "node_modules")
	sharedTarget, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink post-Link: %v", err)
	}

	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if err := RelinkIsolate(repo, []string{"node"}, opts); err != nil {
		t.Fatalf("RelinkIsolate: %v", err)
	}

	iso, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	entry, ok := isIsolated(iso, repo.Root, "node_modules")
	if !ok {
		t.Fatalf("isolation registry has no entry for node_modules; fixture broken")
	}

	return isolatedNodeFixture{
		repo:         repo,
		opts:         opts,
		wsPath:       wsPath,
		sharedTarget: sharedTarget,
		entry:        entry,
	}
}

// planActionStrings flattens every path a plan's actions mention so
// tests can assert none references the isolated variant.
func planActionStrings(plan planner.Plan) []string {
	var out []string
	for _, pa := range plan.Actions {
		switch a := pa.Action.(type) {
		case planner.CreateDirectory:
			out = append(out, a.Path)
		case planner.Move:
			out = append(out, a.Source, a.Destination)
		case planner.Remove:
			out = append(out, a.Path)
		case planner.Symlink:
			out = append(out, a.Link, a.Target)
		default:
			out = append(out, fmt.Sprintf("%+v", pa.Action))
		}
	}
	return out
}

// symlinkActions extracts the Symlink actions from a plan in order.
func symlinkActions(plan planner.Plan) []planner.Symlink {
	var out []planner.Symlink
	for _, pa := range plan.Actions {
		if s, ok := pa.Action.(planner.Symlink); ok {
			out = append(out, s)
		}
	}
	return out
}

// readFileOrEmpty reads path, treating a missing file as empty bytes —
// registry files legitimately may not exist yet.
func readFileOrEmpty(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestBuildRelinkPlanIsolatedResourceExitsToShared pins the core
// BuildRelinkPlan contract for an isolated resource: the exit carries
// the registry's StoragePath (that exact directory gets deleted — a
// wrong path would destroy someone else's variant), the reconnect
// actions target the fingerprint variant rather than the doomed
// isolated path, and a registry entry whose relpath left the config
// is surfaced as SkippedIsolation instead of being exited.
func TestBuildRelinkPlanIsolatedResourceExitsToShared(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	// A registry entry for a resource missing from .wrk.yml: relink
	// cannot build a plan for it and must leave it untouched.
	if err := recordIsolation(fx.repo, fx.repo.Root, "ghost/path", "/nowhere/isolated-dead"); err != nil {
		t.Fatalf("recordIsolation(ghost): %v", err)
	}

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}

	if len(plan.IsolationExits) != 1 {
		t.Fatalf("IsolationExits: got %d want 1: %+v", len(plan.IsolationExits), plan.IsolationExits)
	}
	exit := plan.IsolationExits[0]
	if exit.ResourceName != "node" || exit.ResourcePath != "node_modules" {
		t.Errorf("exit identity: %+v, want node/node_modules", exit)
	}
	if exit.StoragePath != fx.entry.StoragePath {
		t.Errorf("exit StoragePath = %q, want registry's %q", exit.StoragePath, fx.entry.StoragePath)
	}

	if len(plan.SkippedIsolation) != 1 || plan.SkippedIsolation[0] != "ghost/path" {
		t.Errorf("SkippedIsolation = %v, want [ghost/path]", plan.SkippedIsolation)
	}

	if plan.Plan.HasConflicts() {
		t.Fatalf("unexpected conflicts: %+v", plan.Plan.Conflicts)
	}

	// The reconnect targets the fingerprint variant, never the
	// isolated path the exit is about to delete.
	links := symlinkActions(plan.Plan)
	if len(links) != 1 {
		t.Fatalf("symlink actions: got %d want 1 (all actions: %v)",
			len(links), planActionStrings(plan.Plan))
	}
	if links[0].Link != fx.wsPath {
		t.Errorf("symlink Link = %q, want workspace path %q", links[0].Link, fx.wsPath)
	}
	if links[0].Target != fx.sharedTarget {
		t.Errorf("symlink Target = %q, want fingerprint variant %q", links[0].Target, fx.sharedTarget)
	}
	for _, s := range planActionStrings(plan.Plan) {
		if strings.Contains(s, "isolated-") {
			t.Errorf("plan action references the isolated variant: %q", s)
		}
	}
}

// TestBuildRelinkPlanUnfingerprintedIsolationParent pins the
// sharedExistsBesidesIsolation probe through the plan for an
// un-fingerprinted resource, whose isolated variants nest INSIDE the
// shared path. A storage dir holding only isolated-*/bookkeeping
// children is an empty husk: linking to it would hand the workspace
// a hollow directory, so the plan must take the provision path (here:
// conflict, since there is no initialize hook and no workspace copy).
// Real sibling content flips the probe and the plan links.
func TestBuildRelinkPlanUnfingerprintedIsolationParent(t *testing.T) {
	build := func(t *testing.T, seedSibling bool) (RelinkPlan, string, string) {
		t.Helper()
		repo := newTestRepo(t)
		storage := storageIn(t, repo.Root)
		writeConfig(t, repo.Root, config.Filename,
			"resources:\n  - name: node\n    path: node_modules\n")

		storageDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
		variant := filepath.Join(storageDir, "isolated-feedfacefeedface")
		writeFile(t, filepath.Join(variant, "pkg", "marker"), "iso\n")
		// Bookkeeping scratch must not count as shared content.
		writeFile(t, filepath.Join(storageDir, "stale.wrk-lock"), "")
		if seedSibling {
			writeFile(t, filepath.Join(storageDir, "config.json"), "{}\n")
		}

		wsPath := filepath.Join(repo.Root, "node_modules")
		if err := os.Symlink(variant, wsPath); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := recordIsolation(repo, repo.Root, "node_modules", variant); err != nil {
			t.Fatalf("recordIsolation: %v", err)
		}

		plan, err := BuildRelinkPlan(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
		if err != nil {
			t.Fatalf("BuildRelinkPlan: %v", err)
		}
		if len(plan.IsolationExits) != 1 {
			t.Fatalf("IsolationExits: got %d want 1", len(plan.IsolationExits))
		}
		return plan, storageDir, wsPath
	}

	t.Run("only isolated and bookkeeping children conflicts", func(t *testing.T) {
		plan, _, _ := build(t, false)

		if !plan.Plan.HasConflicts() {
			t.Fatalf("husk storage dir counted as shared content; actions: %v",
				planActionStrings(plan.Plan))
		}
		c := plan.Plan.Conflicts[0]
		if c.Instance.RelativePath != "node_modules" {
			t.Errorf("conflict instance = %q, want node_modules", c.Instance.RelativePath)
		}
		if !strings.Contains(c.Message, "no initialize hook") {
			t.Errorf("conflict message = %q, want the no-initialize-hook provision refusal", c.Message)
		}
	})

	t.Run("real sibling content links to the shared dir", func(t *testing.T) {
		plan, storageDir, wsPath := build(t, true)

		if plan.Plan.HasConflicts() {
			t.Fatalf("real sibling content still conflicted: %+v", plan.Plan.Conflicts)
		}
		links := symlinkActions(plan.Plan)
		if len(links) != 1 {
			t.Fatalf("symlink actions: got %d want 1 (all actions: %v)",
				len(links), planActionStrings(plan.Plan))
		}
		if links[0].Link != wsPath || links[0].Target != storageDir {
			t.Errorf("symlink = %+v, want %s -> %s", links[0], wsPath, storageDir)
		}
		for _, s := range planActionStrings(plan.Plan) {
			if strings.Contains(s, "isolated-") {
				t.Errorf("plan action references the isolated variant: %q", s)
			}
		}
	})
}

// TestExecuteRelinkExitsIsolationRoundtrip pins the full isolated →
// linked roundtrip: the workspace symlink lands back on the
// fingerprint variant with the shared bytes, the isolated variant and
// its scratch files are gone from storage, and both registries drop
// their entries.
func TestExecuteRelinkExitsIsolationRoundtrip(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	// Diverge the isolated variant (writes travel through the
	// workspace symlink into isolated storage) so the discard is
	// observable: after relink the marker MUST read the shared bytes.
	writeFile(t, filepath.Join(fx.wsPath, "pkg", "marker"), "divergent\n")

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}
	if err := ExecuteRelink(fx.repo, plan, fx.opts); err != nil {
		t.Fatalf("ExecuteRelink: %v", err)
	}

	target, err := os.Readlink(fx.wsPath)
	if err != nil {
		t.Fatalf("readlink post-relink: %v", err)
	}
	if target != fx.sharedTarget {
		t.Errorf("symlink target = %q, want fingerprint variant %q", target, fx.sharedTarget)
	}
	if strings.Contains(target, "isolated-") {
		t.Errorf("symlink still points into an isolated variant: %q", target)
	}

	got, err := os.ReadFile(filepath.Join(fx.wsPath, "pkg", "marker"))
	if err != nil {
		t.Fatalf("read marker via relinked symlink: %v", err)
	}
	if string(got) != "v1\n" {
		t.Errorf("marker = %q, want shared %q (isolated divergence must be discarded)", got, "v1\n")
	}

	// The variant and every scratch artifact of its deletion are gone.
	for _, p := range []string{
		fx.entry.StoragePath,
		fx.entry.StoragePath + ".wrk-deleting",
		fx.entry.StoragePath + ".wrk-lock",
	} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present after relink (err=%v)", p, err)
		}
	}

	iso, err := loadIsolation(fx.repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(iso, fx.repo.Root, "node_modules"); ok {
		t.Errorf("isolation registry still lists node_modules after relink")
	}

	if entry := readRegistry(t, fx.repo)[fx.repo.Root]; entry != nil {
		t.Errorf("detach registry still has an entry after relink: %v", entry)
	}
}

// TestExecuteRelinkDryRunMutatesNothing pins the dry-run contract on
// the isolation path specifically: symlink, isolated variant, and
// both registry files stay byte-identical. Isolation exits are
// destructive and irreversible — a dry-run that leaked even one step
// would destroy non-reproducible per-workspace bytes.
func TestExecuteRelinkDryRunMutatesNothing(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	markerPath := filepath.Join(fx.entry.StoragePath, "pkg", "marker")
	beforeLink, err := os.Readlink(fx.wsPath)
	if err != nil {
		t.Fatalf("baseline readlink: %v", err)
	}
	beforeMarker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("baseline marker read: %v", err)
	}
	beforeIso := readFileOrEmpty(t, isolationPath(fx.repo))
	beforeDet := readFileOrEmpty(t, registryPath(fx.repo))

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}
	dry := fx.opts
	dry.DryRun = true
	if err := ExecuteRelink(fx.repo, plan, dry); err != nil {
		t.Fatalf("ExecuteRelink(dry-run): %v", err)
	}

	afterLink, err := os.Readlink(fx.wsPath)
	if err != nil {
		t.Fatalf("post readlink: %v", err)
	}
	if afterLink != beforeLink {
		t.Errorf("dry-run repointed the symlink: %q -> %q", beforeLink, afterLink)
	}
	afterMarker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Errorf("dry-run deleted the isolated variant: %v", err)
	} else if !bytes.Equal(afterMarker, beforeMarker) {
		t.Errorf("dry-run changed variant bytes: %q -> %q", beforeMarker, afterMarker)
	}
	if got := readFileOrEmpty(t, isolationPath(fx.repo)); !bytes.Equal(got, beforeIso) {
		t.Errorf("dry-run rewrote isolated.json:\nbefore: %s\nafter:  %s", beforeIso, got)
	}
	if got := readFileOrEmpty(t, registryPath(fx.repo)); !bytes.Equal(got, beforeDet) {
		t.Errorf("dry-run rewrote detached.json:\nbefore: %s\nafter:  %s", beforeDet, got)
	}
}

// TestExecuteRelinkConflictAbortsBeforeIsolationExits pins the
// execution order guarantee: conflicts abort BEFORE any exit runs, so
// a conflicted plan never destroys an isolated variant. The isolated
// resource itself is fine here — an unrelated second resource
// conflicts — yet its variant must survive untouched.
func TestExecuteRelinkConflictAbortsBeforeIsolationExits(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	// A second resource that can only conflict: no workspace copy, no
	// shared copy, no initialize hook.
	writeConfig(t, fx.repo.Root, config.Filename,
		isolateConfigYAML+"  - name: data\n    path: data-dir\n")

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}
	if !plan.Plan.HasConflicts() {
		t.Fatalf("fixture built no conflict; test premise broken")
	}
	if len(plan.IsolationExits) != 1 {
		t.Fatalf("IsolationExits: got %d want 1", len(plan.IsolationExits))
	}

	err = ExecuteRelink(fx.repo, plan, fx.opts)
	if err == nil {
		t.Fatalf("ExecuteRelink succeeded despite conflicts")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error = %v, want a conflict refusal", err)
	}

	// No exit ran: variant on disk, registry entry intact, workspace
	// symlink still pointing into the isolated variant.
	if _, err := os.Stat(filepath.Join(fx.entry.StoragePath, "pkg", "marker")); err != nil {
		t.Errorf("isolated variant touched despite conflict abort: %v", err)
	}
	iso, err := loadIsolation(fx.repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(iso, fx.repo.Root, "node_modules"); !ok {
		t.Errorf("isolation registry entry cleared despite conflict abort")
	}
	target, err := os.Readlink(fx.wsPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if !strings.Contains(target, "isolated-") {
		t.Errorf("workspace symlink repointed despite conflict abort: %q", target)
	}
}

// TestExecuteRelinkRefusesRealDirAtWorkspacePath pins the exit's
// user-data guard: when the workspace path was replaced by a real
// directory (the user un-symlinked it by hand), the exit refuses with
// an error naming the kind of entry found, and neither the user's
// bytes nor the isolated variant are touched.
func TestExecuteRelinkRefusesRealDirAtWorkspacePath(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	if err := os.Remove(fx.wsPath); err != nil {
		t.Fatalf("removing fixture symlink: %v", err)
	}
	writeFile(t, filepath.Join(fx.wsPath, "keep.txt"), "user data\n")

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}

	err = ExecuteRelink(fx.repo, plan, fx.opts)
	if err == nil {
		t.Fatalf("ExecuteRelink succeeded over a real directory at the workspace path")
	}
	if !strings.Contains(err.Error(), "real") {
		t.Errorf("error = %v, want a refusal describing the real entry", err)
	}

	// Variant intact, registry intact, user bytes intact.
	if _, err := os.Stat(filepath.Join(fx.entry.StoragePath, "pkg", "marker")); err != nil {
		t.Errorf("isolated variant damaged by refused exit: %v", err)
	}
	iso, err := loadIsolation(fx.repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(iso, fx.repo.Root, "node_modules"); !ok {
		t.Errorf("isolation registry entry cleared by refused exit")
	}
	got, err := os.ReadFile(filepath.Join(fx.wsPath, "keep.txt"))
	if err != nil {
		t.Fatalf("user file gone after refused exit: %v", err)
	}
	if string(got) != "user data\n" {
		t.Errorf("user file rewritten: %q", got)
	}
}

// TestExecuteRelinkLockedVariantRefusesNamingPath pins the flock
// guard: a held <variant>.wrk-lock means another wrk process owns the
// path, so the exit errors (naming the path so the user knows what to
// retry) and leaves the variant alone.
func TestExecuteRelinkLockedVariantRefusesNamingPath(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	lock := flock.New(fx.entry.StoragePath + ".wrk-lock")
	ok, err := lock.TryLock()
	if err != nil || !ok {
		t.Fatalf("could not hold variant lock: ok=%v err=%v", ok, err)
	}
	defer func() { _ = lock.Unlock() }()

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}

	err = ExecuteRelink(fx.repo, plan, fx.opts)
	if err == nil {
		t.Fatalf("ExecuteRelink deleted a locked variant")
	}
	if !strings.Contains(err.Error(), fx.entry.StoragePath) {
		t.Errorf("error does not name the locked path %q: %v", fx.entry.StoragePath, err)
	}

	if _, err := os.Stat(filepath.Join(fx.entry.StoragePath, "pkg", "marker")); err != nil {
		t.Errorf("locked variant content missing: %v", err)
	}
	iso, err := loadIsolation(fx.repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(iso, fx.repo.Root, "node_modules"); !ok {
		t.Errorf("isolation registry entry cleared despite refused delete")
	}
}

// TestExecuteRelinkIdempotentAfterPartialExit pins crash recovery: a
// prior exit that deleted the variant but died before clearing the
// registry (dangling workspace symlink, stale isolated.json entry)
// is finished by a fresh BuildRelinkPlan + ExecuteRelink — no error
// on the already-gone variant, registry cleared, workspace relinked
// to shared.
func TestExecuteRelinkIdempotentAfterPartialExit(t *testing.T) {
	fx := newIsolatedNodeFixture(t)

	if err := os.RemoveAll(fx.entry.StoragePath); err != nil {
		t.Fatalf("simulating partial exit: %v", err)
	}

	plan, err := BuildRelinkPlan(fx.repo, fx.opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}
	if len(plan.IsolationExits) != 1 {
		t.Fatalf("registry-driven exit missing from rerun plan: %+v", plan.IsolationExits)
	}

	if err := ExecuteRelink(fx.repo, plan, fx.opts); err != nil {
		t.Fatalf("rerun after partial exit: %v", err)
	}

	iso, err := loadIsolation(fx.repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(iso, fx.repo.Root, "node_modules"); ok {
		t.Errorf("isolation registry entry survived the rerun")
	}

	target, err := os.Readlink(fx.wsPath)
	if err != nil {
		t.Fatalf("readlink post-rerun: %v", err)
	}
	if target != fx.sharedTarget {
		t.Errorf("symlink target = %q, want %q", target, fx.sharedTarget)
	}
	got, err := os.ReadFile(filepath.Join(fx.wsPath, "pkg", "marker"))
	if err != nil {
		t.Fatalf("read marker post-rerun: %v", err)
	}
	if string(got) != "v1\n" {
		t.Errorf("marker = %q, want shared %q", got, "v1\n")
	}
}

// TestExecuteRelinkTidiesEmptyStorageParents pins the post-exit tidy
// for an un-fingerprinted resource: when the storage subtree existed
// only as the isolated variant's parent chain, the exit removes the
// now-empty directories up to — but never including — the repo's own
// storage root. The resource is create:false so nothing re-provisions
// the subtree and the tidy is observable end to end.
func TestExecuteRelinkTidiesEmptyStorageParents(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: nm\n"+
			"    path: packages/app/node_modules\n"+
			"    create: false\n")

	repoStorageRoot := filepath.Join(storage, repo.RepositoryID)
	storageDir := filepath.Join(repoStorageRoot, "packages", "app", "node_modules")
	variant := filepath.Join(storageDir, "isolated-cafecafecafecafe")
	writeFile(t, filepath.Join(variant, "pkg", "marker"), "iso\n")

	wsPath := filepath.Join(repo.Root, "packages", "app", "node_modules")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(variant, wsPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := recordIsolation(repo, repo.Root, "packages/app/node_modules", variant); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	plan, err := BuildRelinkPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}
	if len(plan.IsolationExits) != 1 {
		t.Fatalf("IsolationExits: got %d want 1", len(plan.IsolationExits))
	}
	if plan.Plan.HasConflicts() {
		t.Fatalf("create:false resource must skip provisioning quietly, got conflicts: %+v",
			plan.Plan.Conflicts)
	}
	// An exits-only plan is still work: the CLI's nothing-to-do gate
	// must not skip the exit just because Actions is empty.
	if !plan.HasWork() {
		t.Errorf("HasWork() = false with a pending isolation exit")
	}

	if err := ExecuteRelink(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteRelink: %v", err)
	}

	// The whole now-empty chain under <storage>/<repo-id> is tidied…
	if _, err := os.Lstat(filepath.Join(repoStorageRoot, "packages")); !os.IsNotExist(err) {
		t.Errorf("empty storage parents survived the exit (err=%v)", err)
	}
	// …but the repo's storage root itself is preserved.
	if info, err := os.Stat(repoStorageRoot); err != nil || !info.IsDir() {
		t.Errorf("<storage>/<repo-id> removed by parent tidy (err=%v)", err)
	}

	// Workspace symlink removed and nothing re-linked it.
	if _, err := os.Lstat(wsPath); !os.IsNotExist(err) {
		t.Errorf("workspace path still present after exit (err=%v)", err)
	}

	iso, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := isIsolated(iso, repo.Root, "packages/app/node_modules"); ok {
		t.Errorf("isolation registry entry survived the exit")
	}
}

// TestPrintRelinkPlanRendersIsolationSections pins the human preview
// the CLI shows before its destructive confirmation: each exit gets a
// ⚠ line naming the resource and the variant to be discarded under an
// "Isolation exits:" header, and each unmanageable registry entry
// gets a left-untouched note.
func TestPrintRelinkPlanRendersIsolationSections(t *testing.T) {
	var out bytes.Buffer
	plan := RelinkPlan{
		IsolationExits: []IsolationExit{{
			ResourceName: "node",
			ResourcePath: "node_modules",
			StoragePath:  "/store/repo/node_modules/isolated-ab12",
		}},
		SkippedIsolation: []string{"gone/path"},
	}

	if err := PrintRelinkPlan(&out, plan); err != nil {
		t.Fatalf("PrintRelinkPlan: %v", err)
	}
	s := out.String()

	if !strings.Contains(s, "Isolation exits:") {
		t.Errorf("missing exits header:\n%s", s)
	}
	if !strings.Contains(s, "⚠") {
		t.Errorf("exit line missing the destructive marker:\n%s", s)
	}
	if !strings.Contains(s, "[node]") {
		t.Errorf("exit line missing the resource name:\n%s", s)
	}
	if !strings.Contains(s, "/store/repo/node_modules/isolated-ab12") {
		t.Errorf("exit line missing the variant path:\n%s", s)
	}
	if !strings.Contains(s, `"gone/path"`) || !strings.Contains(s, "left untouched") {
		t.Errorf("skipped-isolation note missing:\n%s", s)
	}
}

// TestPrintRelinkPlanPlainOmitsIsolationSections pins the inverse: a
// plan with no isolation involvement renders the planner block only —
// no "Isolation exits:" header, no left-untouched notes — so ordinary
// relinks don't wave isolation warnings at users who never isolated.
func TestPrintRelinkPlanPlainOmitsIsolationSections(t *testing.T) {
	var out bytes.Buffer
	plan := RelinkPlan{
		Plan: planner.Plan{
			WorkspaceRoot: "/wk",
			Actions: []planner.PlannedAction{
				{Action: planner.Symlink{Link: "/wk/.env", Target: "/store/.env"}},
			},
		},
	}

	if err := PrintRelinkPlan(&out, plan); err != nil {
		t.Fatalf("PrintRelinkPlan: %v", err)
	}
	s := out.String()

	if strings.Contains(s, "Isolation exits:") {
		t.Errorf("plain plan rendered an exits header:\n%s", s)
	}
	if strings.Contains(s, "left untouched") {
		t.Errorf("plain plan rendered a skipped-isolation note:\n%s", s)
	}
}
