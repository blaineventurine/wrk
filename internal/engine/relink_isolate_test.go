package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// relinkIsolateFixture returns a fingerprinted `node_modules` resource
// set up for isolate: manifest.json (fingerprint input) is present so
// Link/Detach build against a per-fingerprint storage subdir, which is
// the layout `<storage>/<repo>/<resource>/isolated-<hex>/` is designed
// for — the isolated variant sits alongside the fingerprint variant in
// the same parent, exactly like Task 3.2's registry roundtrip pins.
//
// An un-fingerprinted resource like a leaf `.env` file would collide
// with this layout (the resource path in shared storage is a file, not
// a container of variants); that case is out of scope for Task 3.3 —
// the isolate feature primarily targets fingerprinted directory
// resources where a workspace wants to diverge without dragging peers
// along.
const isolateConfigYAML = "" +
	"resources:\n" +
	"  - name: node\n" +
	"    path: node_modules\n" +
	"    fingerprint:\n" +
	"      - \"{root}/manifest.json\"\n"

func seedIsolateWorkspace(t *testing.T, root, manifest, marker string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "manifest.json"), manifest)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "marker"), marker)
}

// TestRelinkIsolateSwapsDetachedIntoIsolatedStorage pins the core
// outcome of Task 3.3: what was a detached (real) directory becomes a
// symlink into `<storage>/<repo>/<resource>/isolated-<hex>/`, the
// isolation registry records the pin, and the detach registry stops
// listing this resource for this workspace.
func TestRelinkIsolateSwapsDetachedIntoIsolatedStorage(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Sanity: post-Detach the workspace path is a real dir, not a
	// symlink. Without this the swap-test below could pass on the
	// pre-condition alone.
	wsPath := filepath.Join(repo.Root, "node_modules")
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat pre-isolate: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("pre-isolate: workspace node_modules is a symlink; setup broken")
	}

	if err := RelinkIsolate(repo, []string{"node"}, opts); err != nil {
		t.Fatalf("RelinkIsolate: %v", err)
	}

	// Workspace path is now a symlink to isolated-<hex>.
	info, err = os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat post-isolate: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("post-isolate: workspace node_modules is not a symlink; mode=%v", info.Mode())
	}
	target, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(target), "isolated-") {
		t.Errorf("symlink target basename = %q, want isolated-<hex>", filepath.Base(target))
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("isolated variant path %q: stat: %v", target, err)
	}

	// Isolation registry records the pin.
	iso, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	entry, ok := isIsolated(iso, repo.Root, "node_modules")
	if !ok {
		t.Fatalf("isolation registry has no entry for (%s, node_modules)", repo.Root)
	}
	if entry.StoragePath != target {
		t.Errorf("isolation StoragePath = %q, want %q", entry.StoragePath, target)
	}
	if entry.CreatedAt == "" {
		t.Errorf("isolation CreatedAt empty; want RFC3339 timestamp")
	}

	// Detach registry no longer lists this workspace (the only entry
	// was node_modules, so the workspace key is gone).
	det, err := loadRegistry(repo)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	if paths, ok := det[repo.Root]; ok {
		for _, p := range paths {
			if p == "node_modules" {
				t.Errorf("detach registry still lists node_modules for %q", repo.Root)
			}
		}
	}
}

// TestRelinkIsolatePreservesContent pins that RelinkIsolate carries the
// workspace's detached bytes into the isolated variant unmodified.
// Losing bytes here would silently destroy user work.
func TestRelinkIsolatePreservesContent(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Divergence marker: overwrite one file in the detached copy and
	// add a fresh file so we can prove the isolated variant carries
	// THIS state, not the pristine shared bytes.
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg", "marker"), "user-edit\n")
	writeFile(t, filepath.Join(repo.Root, "node_modules", "added.js"), "extra\n")

	if err := RelinkIsolate(repo, []string{"node"}, opts); err != nil {
		t.Fatalf("RelinkIsolate: %v", err)
	}

	// Reading via the workspace path (which is now a symlink) resolves
	// to the isolated variant.
	got, err := os.ReadFile(filepath.Join(repo.Root, "node_modules", "pkg", "marker"))
	if err != nil {
		t.Fatalf("read marker via symlink: %v", err)
	}
	if string(got) != "user-edit\n" {
		t.Errorf("marker = %q, want %q", got, "user-edit\n")
	}
	got, err = os.ReadFile(filepath.Join(repo.Root, "node_modules", "added.js"))
	if err != nil {
		t.Fatalf("read added.js via symlink: %v", err)
	}
	if string(got) != "extra\n" {
		t.Errorf("added.js = %q, want %q", got, "extra\n")
	}
}

// TestRelinkIsolateRefusesLinkedResource pins the preflight: only a
// resource currently detached in THIS workspace can be isolated. A
// linked resource still belongs to shared storage; stealing it would
// silently rob every peer worktree of the variant they pinned.
func TestRelinkIsolateRefusesLinkedResource(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	err := RelinkIsolate(repo, []string{"node"}, opts)
	if err == nil {
		t.Fatalf("expected error for linked resource; got nil")
	}
	if !strings.Contains(err.Error(), "not detached") {
		t.Errorf("error = %v, want mention of 'not detached'", err)
	}
}

// TestRelinkIsolateRefusesUnknownResource pins the config-membership
// preflight: a name not in .wrk.yml is rejected before any filesystem
// mutation.
func TestRelinkIsolateRefusesUnknownResource(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	err := RelinkIsolate(repo, []string{"missing"}, opts)
	if err == nil {
		t.Fatalf("expected error for unknown resource; got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want mention of 'not configured'", err)
	}
}

// TestRelinkIsolateEmptyNamesIsolatesAllDetached pins the empty-names
// shorthand: "isolate everything currently detached in this workspace".
// This is what a bare `wrk relink --isolate` will resolve to via
// Task 3.5.
func TestRelinkIsolateEmptyNamesIsolatesAllDetached(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// Two fingerprinted directory resources so the isolate-all path
	// exercises the loop.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n"+
			"  - name: vendor\n"+
			"    path: vendor\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":1}`)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg", "m"), "n\n")
	writeFile(t, filepath.Join(repo.Root, "vendor", "libs", "m"), "v\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if err := RelinkIsolate(repo, nil, opts); err != nil {
		t.Fatalf("RelinkIsolate: %v", err)
	}

	iso, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	entries, ok := iso[repo.Root]
	if !ok {
		t.Fatalf("no isolation entries for %q", repo.Root)
	}
	for _, p := range []string{"node_modules", "vendor"} {
		if _, has := entries[p]; !has {
			t.Errorf("isolation registry missing %q", p)
		}
	}

	// Both symlinked into isolated storage.
	for _, p := range []string{"node_modules", "vendor"} {
		wsPath := filepath.Join(repo.Root, p)
		info, err := os.Lstat(wsPath)
		if err != nil {
			t.Fatalf("lstat %s: %v", p, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("workspace %s not a symlink after isolate; mode=%v", p, info.Mode())
		}
	}
}

// TestRelinkIsolateEmptyNamesNothingDetachedErrors pins the guard that
// prevents a bare `wrk relink --isolate` from silently no-op'ing when
// the user thought they had something to isolate.
func TestRelinkIsolateEmptyNamesNothingDetachedErrors(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	// No Detach: nothing is detached.

	err := RelinkIsolate(repo, nil, opts)
	if err == nil {
		t.Fatalf("expected error when nothing is detached; got nil")
	}
	if !strings.Contains(err.Error(), "no detached resources") {
		t.Errorf("error = %v, want mention of 'no detached resources'", err)
	}
}

// TestRelinkIsolatePeersUntouched pins the per-workspace isolation
// contract: RelinkIsolate in workspace A must not repoint the symlink
// or alter the bytes seen by workspace B. B's link still points at the
// shared variant B pinned; only A's link moves.
//
// This is the whole point of the feature — a workspace that wants to
// diverge from the shared variant without dragging every peer along.
func TestRelinkIsolatePeersUntouched(t *testing.T) {
	primary := newTestRepoWithHead(t, map[string]string{
		".wrk.yml":      isolateConfigYAML,
		"manifest.json": `{"v":1}`,
	})
	_, feature := addGitWorktree(t, primary, "feature")

	// Storage under the primary; both worktrees share it — the
	// realistic layout.
	storage := storageIn(t, primary.Root)

	// Seed only the primary's workspace content; primary Link
	// provisions the shared variant. feature has the same manifest.json
	// (inherited from HEAD via `git worktree add`) so its Link picks the
	// same fingerprint and simply installs a symlink to the primary-
	// provisioned variant. Seeding feature's node_modules too would trip
	// a "workspace has content AND shared exists" conflict on Link.
	writeFile(t, filepath.Join(primary.Root, "node_modules", "pkg", "m"), "shared\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(primary, opts); err != nil {
		t.Fatalf("Link(primary): %v", err)
	}
	if err := Link(feature, opts); err != nil {
		t.Fatalf("Link(feature): %v", err)
	}

	// Snapshot the peer's symlink target BEFORE the isolate.
	peerBefore, err := os.Readlink(filepath.Join(feature.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink feature/node_modules pre-isolate: %v", err)
	}

	// In the primary, Detach then RelinkIsolate.
	if err := Detach(primary, opts); err != nil {
		t.Fatalf("Detach(primary): %v", err)
	}
	if err := RelinkIsolate(primary, []string{"node"}, opts); err != nil {
		t.Fatalf("RelinkIsolate(primary): %v", err)
	}

	// Peer's symlink target unchanged.
	peerAfter, err := os.Readlink(filepath.Join(feature.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink feature/node_modules post-isolate: %v", err)
	}
	if peerAfter != peerBefore {
		t.Errorf("peer symlink target changed: before=%q, after=%q",
			peerBefore, peerAfter)
	}

	// Peer's content unchanged.
	peerContent, err := os.ReadFile(filepath.Join(feature.Root, "node_modules", "pkg", "m"))
	if err != nil {
		t.Fatalf("read feature/node_modules/pkg/m post-isolate: %v", err)
	}
	if string(peerContent) != "shared\n" {
		t.Errorf("peer content = %q, want %q (shared bytes untouched)",
			peerContent, "shared\n")
	}

	// Isolation registry is keyed by PRIMARY.Root only; feature's
	// Root MUST NOT appear.
	iso, err := loadIsolation(primary)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := iso[feature.Root]; ok {
		t.Errorf("isolation registry has entry for feature.Root; want none")
	}
	if _, ok := iso[primary.Root]; !ok {
		t.Errorf("isolation registry missing entry for primary.Root")
	}
}

// TestRelinkIsolateDryRunNoMutation pins the dry-run contract: no
// registry entry, no filesystem change, informative stdout.
func TestRelinkIsolateDryRunNoMutation(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	var out bytes.Buffer
	dryOpts := Options{StorageRoot: storage, Stdout: &out, DryRun: true}
	if err := RelinkIsolate(repo, []string{"node"}, dryOpts); err != nil {
		t.Fatalf("RelinkIsolate dry-run: %v", err)
	}

	// No isolation entry.
	iso, err := loadIsolation(repo)
	if err != nil {
		t.Fatalf("loadIsolation: %v", err)
	}
	if _, ok := iso[repo.Root]; ok {
		t.Errorf("dry-run created isolation entry: %v", iso)
	}

	// Workspace path still a real dir (Detach's output), not a symlink.
	info, err := os.Lstat(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("dry-run flipped workspace node_modules into a symlink; mode=%v", info.Mode())
	}

	// Stdout mentions "Would isolate".
	if !strings.Contains(out.String(), "Would isolate") {
		t.Errorf("dry-run stdout missing 'Would isolate': %q", out.String())
	}
}

// TestRelinkIsolateNilRepoErrors guards the earliest guard-clause. A
// nil repo would deref inside config.Load and produce a confusing
// panic instead of a caller-actionable error.
func TestRelinkIsolateNilRepoErrors(t *testing.T) {
	if err := RelinkIsolate(nil, nil, Options{Stdout: &bytes.Buffer{}}); err == nil {
		t.Fatal("expected error for nil repo")
	}
}

// TestRelinkIsolateIsolatedPathLayout pins the exact on-disk layout of
// the isolated variant: `<storage>/<repositoryID>/<resource path>/isolated-<hex>`.
// gc and doctor navigate this exact tree to keep isolated variants
// alive across sweeps — a layout regression here would silently pin
// nothing.
func TestRelinkIsolateIsolatedPathLayout(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	seedIsolateWorkspace(t, repo.Root, `{"v":1}`, "v1\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
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
		t.Fatalf("no isolation entry")
	}

	expectedPrefix := filepath.Join(storage, repo.RepositoryID, "node_modules") + string(os.PathSeparator) + "isolated-"
	if !strings.HasPrefix(entry.StoragePath, expectedPrefix) {
		t.Errorf("isolated storage path = %q, want prefix %q",
			entry.StoragePath, expectedPrefix)
	}
}
// TestBuildRelinkIsolatePlanUnknownResourceErrors pins that the
// "not configured" refusal survives the Build/Execute split — the
// CLI's Confirm prompt must never appear for a typo'd name.
func TestBuildRelinkIsolatePlanUnknownResourceErrors(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t)
	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)
	_ = root

	_, err := BuildRelinkIsolatePlan(repo, []string{"nope"}, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("BuildRelinkIsolatePlan(unknown): err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `"nope" not configured`) {
		t.Fatalf("error should name the unknown resource, got: %v", err)
	}
}

// TestBuildRelinkIsolatePlanNilRepoErrors pins the nil-guard on the
// split. A nil repo would deref inside config.Load and produce a
// confusing panic instead of a caller-actionable error.
func TestBuildRelinkIsolatePlanNilRepoErrors(t *testing.T) {
	_, err := BuildRelinkIsolatePlan(nil, nil, Options{Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("BuildRelinkIsolatePlan(nil, ...): err = nil, want non-nil")
	}
}

// TestBuildRelinkIsolatePlanNothingDetachedErrors pins that an
// empty resourceNames slice against a workspace with no detach
// entries is a hard error, not a no-op. Users who fired `wrk relink
// --isolate` with nothing to isolate almost certainly meant to
// isolate something; a silent success would hide the mistake.
func TestBuildRelinkIsolatePlanNothingDetachedErrors(t *testing.T) {
	repo := newTestRepo(t)
	writeConfig(t, repo.Root, config.Filename, isolateConfigYAML)

	_, err := BuildRelinkIsolatePlan(repo, nil, Options{
		StorageRoot: storageIn(t, repo.Root),
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("BuildRelinkIsolatePlan(empty, no detached): err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "no detached resources") {
		t.Fatalf("error should mention no detached resources, got: %v", err)
	}
}
