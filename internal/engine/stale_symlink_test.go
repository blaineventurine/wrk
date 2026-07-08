package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/fingerprint"
)

// staleConfig is a single-resource, fingerprint-gated config: the node
// resource's shared location keys off manifest.json, so mutating the
// manifest re-keys the shared path and turns any pre-existing symlink
// into a "stale" pointer. `{root}` (absolute) is important: it makes
// the fingerprint compute against the file on disk regardless of the
// test-binary CWD, which the relative-path form does not guarantee.
const staleConfig = "resources:\n" +
	"  - name: node\n" +
	"    path: node_modules\n" +
	"    fingerprint:\n" +
	"      - \"{root}/manifest.json\"\n"

// seedStaleWorkspace lays down the initial workspace shape: manifest at
// v1 and a workspace node_modules directory holding the v1 payload.
// Callers Link this once to establish the variant-1 symlink, then hand-
// edit manifest to force staleness on the next Status/Link.
func seedStaleWorkspace(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "manifest.json"), `{"v":1}`)
	writeFile(t, filepath.Join(root, "node_modules", "pkg-v1", "index.js"), "v1\n")
}

// fingerprintFor computes the shared-location fingerprint the same way
// location.For does internally: the placeholder-expanded input path is
// absolute (`<root>/manifest.json`), so the test call and the production
// call hash identical bytes and produce identical fingerprints.
func fingerprintFor(t *testing.T, root string) string {
	t.Helper()
	fp, err := fingerprint.Fingerprint(root, filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	return fp
}

// TestStaleSymlinkRepairedByLink pins the "no repair path exists" branch:
// after the manifest changes under a linked resource, `wrk status` MUST
// classify the symlink as stale, and a second `wrk link` with nothing on
// disk to adopt AND no hook to run for the new variant MUST surface the
// stale state as a conflict (rather than silently leaving the wrong
// symlink in place). A regression that reported the stale symlink as
// linked, or that quietly re-pointed at an empty target, would flip
// exactly one of the assertions below.
func TestStaleSymlinkRepairedByLink(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, staleConfig)
	seedStaleWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}

	// Snapshot the variant-1 shared path via the workspace symlink,
	// so the stale-state assertion below doesn't depend on recomputing
	// the fingerprint (any mismatch would prove staleness).
	workspaceLink := filepath.Join(repo.Root, "node_modules")
	variant1Target, err := os.Readlink(workspaceLink)
	if err != nil {
		t.Fatalf("readlink #1: %v", err)
	}
	fpV1 := fingerprintFor(t, repo.Root)
	wantV1, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "node_modules", fpV1))
	if err != nil {
		t.Fatal(err)
	}
	if variant1Target != wantV1 {
		t.Fatalf("variant-1 target = %q, want %q (setup wired wrong)", variant1Target, wantV1)
	}

	// Bump the manifest. Fingerprint recomputes to a different value,
	// so location.For returns a different shared path, but the
	// workspace symlink still points at the old one -> stale.
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)

	report, err := Status(repo, opts)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	if got := report.Rows[0].State; got != StateStale {
		t.Errorf("Status state = %q, want %q (fingerprint mismatch must surface as stale)", got, StateStale)
	}

	// Workspace still IS a symlink pointing at variant-1 — until Link
	// runs, nothing on disk has been mutated.
	info, err := os.Lstat(workspaceLink)
	if err != nil {
		t.Fatalf("lstat pre-Link #2: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("workspace path is no longer a symlink pre-Link #2; mode=%v", info.Mode())
	}
	if pre, err := os.Readlink(workspaceLink); err != nil || pre != variant1Target {
		t.Errorf("workspace target changed before Link #2: got=%q err=%v, want %q", pre, err, variant1Target)
	}

	// Second Link. There's no workspace node_modules content to adopt
	// (the symlink is not a directory) and no initialize hook, so the
	// planner has no way to provision variant-2 -> conflict.
	var out bytes.Buffer
	err = Link(repo, Options{StorageRoot: storage, Stdout: &out})
	if err == nil {
		t.Fatalf("Link #2 succeeded; want conflict\nstdout:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("Link #2 error = %q, want to contain %q", err.Error(), "conflict")
	}
}

// TestStaleSymlinkRepairedWhenNewVariantExists pins the "peer already
// provisioned the new variant" branch: another workspace (or the user)
// pre-created variant-2 shared, so a second Link on THIS workspace
// simply re-points the symlink. Success is defined by three
// invariants: no error, the symlink now targets variant-2 exactly, and
// the variant-1 subdirectory is left in place (Link never garbage-
// collects old variants).
func TestStaleSymlinkRepairedWhenNewVariantExists(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, staleConfig)
	seedStaleWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}
	fpV1 := fingerprintFor(t, repo.Root)
	variant1Shared, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "node_modules", fpV1))
	if err != nil {
		t.Fatal(err)
	}

	// Bump manifest -> fingerprint changes -> workspace symlink is stale.
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)

	// Simulate a peer that ran Link earlier and provisioned variant-2.
	fpV2 := fingerprintFor(t, repo.Root)
	if fpV2 == fpV1 {
		t.Fatalf("fingerprint unchanged after manifest bump (v1=%q, v2=%q) — test premise broken", fpV1, fpV2)
	}
	variant2Shared, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, "node_modules", fpV2))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(variant2Shared, "pkg-v2", "index.js"), "v2\n")

	// Second Link now finds shared-exists at variant-2 -> plan swaps
	// the stale symlink for a fresh one. No conflict, no hook needed.
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #2: %v", err)
	}

	workspaceLink := filepath.Join(repo.Root, "node_modules")
	newTarget, err := os.Readlink(workspaceLink)
	if err != nil {
		t.Fatalf("readlink after Link #2: %v", err)
	}
	if newTarget != variant2Shared {
		t.Errorf("post-repair target = %q, want %q", newTarget, variant2Shared)
	}
	// Read through the repaired symlink to be sure it resolves to the
	// v2 payload, not just the right path string.
	if got, err := os.ReadFile(filepath.Join(workspaceLink, "pkg-v2", "index.js")); err != nil || string(got) != "v2\n" {
		t.Errorf("through repaired symlink: got=%q err=%v, want %q", got, err, "v2\n")
	}

	// Variant-1 shared subdirectory MUST still exist on disk — Link
	// never prunes old variants, and a regression that did would
	// break every peer still pointing at the old fingerprint.
	if _, err := os.Stat(variant1Shared); err != nil {
		t.Errorf("variant-1 shared %s vanished after repair: %v", variant1Shared, err)
	}
}

// TestStaleSymlinkRepairedByRelink pins that Relink's discard-local
// override does NOT paper over an unprovisionable stale state: with
// no local copy to discard AND no way to build variant-2 (no shared,
// no hook), Relink surfaces the same conflict Link does. This is the
// pinned behavior of the current buildLink switch — a regression that
// silently made Relink succeed here (leaving the workspace pointing
// at the wrong shared path) would be worse than the conflict.
func TestStaleSymlinkRepairedByRelink(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, staleConfig)
	seedStaleWorkspace(t, repo.Root)

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)

	var out bytes.Buffer
	err := Relink(repo, Options{StorageRoot: storage, Stdout: &out})
	if err == nil {
		t.Fatalf("Relink succeeded; want conflict\nstdout:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("Relink error = %q, want to contain %q", err.Error(), "conflict")
	}
}
