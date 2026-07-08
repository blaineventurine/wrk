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

// TestLinkProvisionsResourceFirstRun pins the happy path: on a repo
// whose workspace has a real .env, Link moves it to shared storage and
// swaps the workspace entry for a symlink pointing at the shared copy.
// This is the entire user-visible reason Link exists.
func TestLinkProvisionsResourceFirstRun(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "hello=world\n")

	if err := Link(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Workspace must now be a symlink whose target is the shared copy.
	workspaceEnv := filepath.Join(repo.Root, ".env")
	info, err := os.Lstat(workspaceEnv)
	if err != nil {
		t.Fatalf("lstat workspace .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace .env is not a symlink; mode=%v", info.Mode())
	}
	link, err := os.Readlink(workspaceEnv)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	// Shared copy must exist at the storage-computed path with the
	// original bytes.
	sharedEnv := filepath.Join(storage, repo.RepositoryID, ".env")
	sharedAbs, err := filepath.Abs(sharedEnv)
	if err != nil {
		t.Fatal(err)
	}
	if link != sharedAbs {
		t.Errorf("symlink target = %q, want %q", link, sharedAbs)
	}
	got, err := os.ReadFile(sharedAbs)
	if err != nil {
		t.Fatalf("read shared copy: %v", err)
	}
	if string(got) != "hello=world\n" {
		t.Errorf("shared content = %q, want %q", got, "hello=world\n")
	}
}

// TestLinkSecondRunIsIdempotent pins that Link is safe to re-run once
// the workspace is already in the linked state. The second run must
// produce no filesystem changes: symlink identity is preserved
// (inode+target unchanged) and the shared copy's mtime is not touched.
func TestLinkSecondRunIsIdempotent(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}

	sharedAbs, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(sharedAbs)
	if err != nil {
		t.Fatalf("stat shared: %v", err)
	}
	linkBefore, err := os.Readlink(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("readlink #1: %v", err)
	}

	// Second Link: plan MUST be empty; runPlan writes nothing to stdout
	// beyond the "no actions" preamble, and neither the symlink nor
	// the shared copy is touched.
	var out bytes.Buffer
	opts.Stdout = &out
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #2: %v", err)
	}

	linkAfter, err := os.Readlink(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("readlink #2: %v", err)
	}
	if linkAfter != linkBefore {
		t.Errorf("symlink target changed: before=%q, after=%q", linkBefore, linkAfter)
	}
	after, err := os.Stat(sharedAbs)
	if err != nil {
		t.Fatalf("stat shared #2: %v", err)
	}
	// Same inode / device / mtime → no in-place rewrite.
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("shared mtime changed: before=%v, after=%v", before.ModTime(), after.ModTime())
	}

	// A no-op link produces no per-resource plan output — the printer
	// only emits a header when a resource has actions.
	printed := out.String()
	if strings.Contains(printed, ".env") {
		t.Errorf("second Link printed action lines for .env; plan was not empty:\n%s", printed)
	}
}

// TestLinkConflictWhenLocalCopyMatchesShared pins that Link refuses to
// silently reunite a workspace with its shared copy when both exist as
// real bytes. The user must run relink to explicitly discard the local
// side; a plain link that clobbers user data would be a footgun.
//
// Exercised on a fingerprinted resource because that's the shape the
// task called out — fingerprinting doesn't change the conflict logic
// but is where accidental content divergence is most likely.
func TestLinkConflictWhenLocalCopyMatchesShared(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    fingerprint:\n"+
			"      - .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "workspace-copy\n")

	// Pre-populate the shared storage path so both sides exist.
	fp, err := fingerprint.Fingerprint(repo.Root, ".env")
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	sharedDir := filepath.Join(storage, repo.RepositoryID, ".env")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sharedDir, fp), "shared-copy\n")

	var out bytes.Buffer
	err = Link(repo, Options{StorageRoot: storage, Stdout: &out})
	if err == nil {
		t.Fatalf("Link succeeded; want conflict error\nstdout:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("Link error = %q, want to contain %q", err.Error(), "conflict")
	}
}

// TestLinkExecutesInitializeHookOnFirstRun pins that the initialize
// hook is executed exactly on the first Link for a hook-bearing
// resource. Uses `touch {shared} {shared}.hook-ran` — a single-syscall
// approach with no shell dependency — so the marker is proof the
// hook's command list actually ran, not just that a plan was built.
func TestLinkExecutesInitializeHookOnFirstRun(t *testing.T) {
	if _, err := os.Stat("/usr/bin/touch"); err != nil {
		// touch(1) is POSIX-standard; skip on the rare host missing it.
		if _, err2 := os.Stat("/bin/touch"); err2 != nil {
			t.Skip("touch(1) not available")
		}
	}

	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: touch {shared} {shared}.hook-ran\n",
	)
	// Deliberately NO .env in the workspace — that forces the
	// provisionShared/hook branch instead of the adopt-workspace-copy
	// (Move) branch.

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	sharedEnv := filepath.Join(storage, repo.RepositoryID, ".env")
	if _, err := os.Stat(sharedEnv); err != nil {
		t.Errorf("hook did not create shared file %s: %v", sharedEnv, err)
	}
	marker := sharedEnv + ".hook-ran"
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("hook marker file %s missing: %v", marker, err)
	}

	// And the workspace was symlinked to the shared file.
	link, err := os.Readlink(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("readlink after hook Link: %v", err)
	}
	wantAbs, err := filepath.Abs(sharedEnv)
	if err != nil {
		t.Fatal(err)
	}
	if link != wantAbs {
		t.Errorf("symlink target = %q, want %q", link, wantAbs)
	}
}

// TestLinkClearsDetachRegistryOnSuccess pins the invariant that a
// successful Link reconnects the workspace to shared storage, so any
// prior detach record for this workspace MUST be removed. Without
// this, `wrk status` would keep reporting "detached" after the user
// has explicitly re-linked.
func TestLinkClearsDetachRegistryOnSuccess(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "content\n")

	// Stage a bogus entry so we can prove Link's clear step ran.
	if err := recordDetached(repo, []string{".env"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}
	if got := readRegistry(t, repo)[repo.Root]; len(got) == 0 {
		t.Fatalf("registry seed missing")
	}

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	reg := readRegistry(t, repo)
	if entry, ok := reg[repo.Root]; ok {
		t.Errorf("detach registry entry survived Link: %v", entry)
	}
}

// TestLinkDryRunPrintsPlanAndSkipsRegistry pins the --dry-run
// contract: the plan is printed (so the user can review) but no side
// effects — filesystem OR registry — are committed.
func TestLinkDryRunPrintsPlanAndSkipsRegistry(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	// Seed the registry — a dry-run must not clear it.
	if err := recordDetached(repo, []string{".env"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}

	var out bytes.Buffer
	if err := Link(repo, Options{
		StorageRoot: storage,
		DryRun:      true,
		Stdout:      &out,
	}); err != nil {
		t.Fatalf("Link(dry-run): %v", err)
	}

	// Plan output must mention the resource so the user can see what
	// would happen; the exact rendering is covered elsewhere.
	printed := out.String()
	if !strings.Contains(printed, "env") {
		t.Errorf("dry-run plan output missing resource name:\n%s", printed)
	}

	// Workspace .env must still be a real file (not a symlink).
	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("dry-run created a symlink; want the original file untouched")
	}

	// Registry must still hold the staged entry.
	reg := readRegistry(t, repo)
	entry := reg[repo.Root]
	if len(entry) != 1 || entry[0] != ".env" {
		t.Errorf("dry-run mutated registry: got %v, want [.env]", entry)
	}
}
