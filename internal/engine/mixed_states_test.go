package engine

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// mixedStateConfig is the three-resource configuration these tests share.
// The YAML order (env, node, bundle) is load-bearing: Status returns rows
// in config order, so any assertion on rows[0..2] pins the resolver
// preserving that order for a mixed-shape config.
//
// - env: create:false, no file on disk anywhere -> StateExpected, no plan
// - node: real workspace directory, no shared -> StateNotLinked, adopts via Move
// - bundle: nothing on disk, has an initialize hook -> StatePending, provisioned via hook
const mixedStateConfig = "resources:\n" +
	"  - name: env\n" +
	"    path: .env\n" +
	"    create: false\n" +
	"  - name: node\n" +
	"    path: node_modules\n" +
	"  - name: bundle\n" +
	"    path: vendor/bundle\n" +
	"    hooks:\n" +
	"      initialize:\n" +
	"        - run: mkdir -p {shared}\n" +
	"        - run: touch {shared}/marker\n"

// skipUnlessMixedHookTooling skips when the two POSIX binaries the
// bundle hook shells out to aren't reachable. Same protective pattern
// TestLinkExecutesInitializeHookOnFirstRun uses for touch(1); the
// mixed-states test also needs mkdir(1) so we look both up.
func skipUnlessMixedHookTooling(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"mkdir", "touch"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s(1) not available on PATH", bin)
		}
	}
}

// seedMixedWorkspace populates a workspace so each of the three
// resources lands in the exact state its name promises before Link:
// env is absent, node_modules is a real directory holding a
// distinguishable payload, and vendor/bundle has nothing to adopt (so
// the hook branch must run).
func seedMixedWorkspace(t *testing.T, root string) {
	t.Helper()
	// node_modules is a real directory with an identifiable child so
	// we can prove Move relocated its contents, not that the executor
	// merely created an empty replacement.
	writeFile(t, filepath.Join(root, "node_modules", "dep-a", "index.js"), "// node dep\n")
	// Deliberately: no .env, no vendor/bundle.
}

// TestLinkThreeResourcesInMixedStates pins that ONE Link call handles
// three disjoint per-resource states correctly and independently:
// create:false stays a no-op, the not-linked real directory gets
// adopted into shared and symlinked back, and the pending hook-driven
// resource is provisioned by running its hook. A regression that
// short-circuits on the first resource — or that runs the hook for
// every resource — would flip exactly one of these three assertions.
func TestLinkThreeResourcesInMixedStates(t *testing.T) {
	skipUnlessMixedHookTooling(t)

	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, mixedStateConfig)
	seedMixedWorkspace(t, repo.Root)

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// env (create:false, nothing anywhere): must remain missing on
	// disk. A regression that "helpfully" created a shared placeholder
	// would flip this.
	envPath := filepath.Join(repo.Root, ".env")
	if _, err := os.Lstat(envPath); !os.IsNotExist(err) {
		t.Errorf(".env should not exist after Link (create:false); err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(storage, repo.RepositoryID, ".env")); !os.IsNotExist(err) {
		t.Errorf("shared .env should not exist after Link (create:false); err=%v", err)
	}

	// node_modules (was a real directory): now a symlink to shared,
	// and the shared side owns the dep-a payload the workspace used to.
	nodeWorkspace := filepath.Join(repo.Root, "node_modules")
	info, err := os.Lstat(nodeWorkspace)
	if err != nil {
		t.Fatalf("lstat node_modules: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("node_modules is not a symlink after Link; mode=%v", info.Mode())
	}
	nodeTarget, err := os.Readlink(nodeWorkspace)
	if err != nil {
		t.Fatalf("readlink node_modules: %v", err)
	}
	wantNodeTarget, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	if nodeTarget != wantNodeTarget {
		t.Errorf("node_modules target = %q, want %q", nodeTarget, wantNodeTarget)
	}
	got, err := os.ReadFile(filepath.Join(wantNodeTarget, "dep-a", "index.js"))
	if err != nil {
		t.Fatalf("read moved node dep: %v", err)
	}
	if string(got) != "// node dep\n" {
		t.Errorf("moved node payload = %q, want %q", got, "// node dep\n")
	}

	// bundle (was absent, hook-driven): now a symlink, shared side
	// was created by the hook, and the marker file the hook touched
	// exists — proof the InitializeResource action actually ran the
	// hook's command list rather than merely being planned.
	bundleWorkspace := filepath.Join(repo.Root, "vendor", "bundle")
	info, err = os.Lstat(bundleWorkspace)
	if err != nil {
		t.Fatalf("lstat vendor/bundle: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("vendor/bundle is not a symlink after Link; mode=%v", info.Mode())
	}
	bundleTarget, err := os.Readlink(bundleWorkspace)
	if err != nil {
		t.Fatalf("readlink vendor/bundle: %v", err)
	}
	wantBundleTarget, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "vendor", "bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if bundleTarget != wantBundleTarget {
		t.Errorf("vendor/bundle target = %q, want %q", bundleTarget, wantBundleTarget)
	}
	if _, err := os.Stat(filepath.Join(wantBundleTarget, "marker")); err != nil {
		t.Errorf("hook marker missing at %s: %v", filepath.Join(wantBundleTarget, "marker"), err)
	}
}

// TestStatusReportsCorrectPerResourceStates pins Status's per-resource
// discrimination in the SAME mixed-config setup, BEFORE any Link.
// Three rows in config order, each state derived from that resource's
// own filesystem shape — a regression that shared a single derived
// state across resources would collapse two of these onto one value.
func TestStatusReportsCorrectPerResourceStates(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, mixedStateConfig)
	seedMixedWorkspace(t, repo.Root)

	report, err := Status(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if got, want := len(report.Rows), 3; got != want {
		t.Fatalf("rows = %d, want %d: %+v", got, want, report.Rows)
	}

	// Row order MUST match the config order — the resolver preserves
	// the Resources slice and Status walks it linearly.
	wants := []struct {
		resource string
		state    State
	}{
		{"env", StateExpected},
		{"node", StateNotLinked},
		{"bundle", StatePending},
	}
	for i, w := range wants {
		row := report.Rows[i]
		if row.Resource != w.resource {
			t.Errorf("rows[%d].Resource = %q, want %q", i, row.Resource, w.resource)
		}
		if row.State != w.state {
			t.Errorf("rows[%d] (%s) state = %q, want %q", i, w.resource, row.State, w.state)
		}
	}
}

// TestDetachAffectsOnlyLinkedResources pins that Detach walks the same
// mixed-state config and only materializes the resources that were
// actually symlinked — the create:false resource never had a symlink
// to break, so it stays out of both the filesystem work AND the detach
// registry. A regression that recorded every resource in the registry
// would flip the .env assertion; one that skipped the hook-provisioned
// bundle would flip the vendor/bundle assertion.
func TestDetachAffectsOnlyLinkedResources(t *testing.T) {
	skipUnlessMixedHookTooling(t)

	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, mixedStateConfig)
	seedMixedWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// env: still absent (nothing to detach for create:false).
	if _, err := os.Lstat(filepath.Join(repo.Root, ".env")); !os.IsNotExist(err) {
		t.Errorf(".env should still not exist after Detach; err=%v", err)
	}

	// node_modules: was a symlink, is now a real directory; child payload survives.
	assertDetachedDirWithChild(t, filepath.Join(repo.Root, "node_modules"),
		filepath.Join("dep-a", "index.js"), "// node dep\n")

	// vendor/bundle: was a symlink, is now a real directory holding
	// the marker the hook created.
	bundleDir := filepath.Join(repo.Root, "vendor", "bundle")
	bundleInfo, err := os.Lstat(bundleDir)
	if err != nil {
		t.Fatalf("lstat vendor/bundle after Detach: %v", err)
	}
	if bundleInfo.Mode()&os.ModeSymlink != 0 {
		t.Errorf("vendor/bundle still a symlink after Detach; mode=%v", bundleInfo.Mode())
	}
	if !bundleInfo.IsDir() {
		t.Errorf("vendor/bundle is not a directory after Detach; mode=%v", bundleInfo.Mode())
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "marker")); err != nil {
		t.Errorf("bundle marker missing after Detach: %v", err)
	}

	// Registry: exactly the two linked resources, in workspace-relative form.
	got := sortedRegistryEntry(readRegistry(t, repo), repo.Root)
	want := []string{"node_modules", "vendor/bundle"}
	if len(got) != len(want) {
		t.Fatalf("registry = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("registry[%d] = %q, want %q", i, got[i], w)
		}
	}
	// Explicit belt-and-braces: .env MUST NOT be in the registry.
	for _, p := range got {
		if p == ".env" {
			t.Errorf("registry contains .env; create:false resources must not be recorded")
		}
	}
}

// assertDetachedDirWithChild verifies a workspace path is a real
// directory (not a symlink) and holds childRel with childContent.
// Extracted so the three mixed-state tests can share the same
// invariant without one drifting from another.
func assertDetachedDirWithChild(t *testing.T, path, childRel, childContent string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s still a symlink; mode=%v", path, info.Mode())
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory; mode=%v", path, info.Mode())
	}
	got, err := os.ReadFile(filepath.Join(path, childRel))
	if err != nil {
		t.Fatalf("read %s/%s: %v", path, childRel, err)
	}
	if string(got) != childContent {
		t.Errorf("%s/%s = %q, want %q", path, childRel, got, childContent)
	}
}
