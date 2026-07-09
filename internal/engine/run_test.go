package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/repository"
)

// linkWithHook seeds a resource whose initialize hook writes a marker
// file at {shared}/marker containing $WRK_TEST_TOKEN. Uses env vars so
// a follow-up Run with a different token proves the hook re-ran (not
// just that stale bytes survived). Returns the Repository, the shared
// variant path, and the storage root so tests can inspect them
// directly.
func linkWithHook(t *testing.T, token string) (*repository.Repository, string, string) {
	t.Helper()

	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'mkdir -p {shared} && printf %s \"$WRK_TEST_TOKEN\" > {shared}/marker'\n"+
			"          env:\n"+
			"            WRK_TEST_TOKEN: "+token+"\n",
	)

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	sharedPath := filepath.Join(storage, repo.RepositoryID, "node_modules")
	return repo, sharedPath, storage
}

// TestRunReRunsHookAgainstExistingVariant pins the primary contract:
// a resource that is already linked and provisioned gets its shared
// variant's contents replaced by a fresh hook run. The variant PATH
// is unchanged (Run does not recompute fingerprints; workspace
// symlink is untouched) — only the bytes behind it are new.
func TestRunReRunsHookAgainstExistingVariant(t *testing.T) {
	repo, sharedPath, storage := linkWithHook(t, "first")

	// Sanity: the hook wrote the seed marker.
	got, err := os.ReadFile(filepath.Join(sharedPath, "marker"))
	if err != nil {
		t.Fatalf("initial marker missing: %v", err)
	}
	if string(got) != "first" {
		t.Fatalf("initial marker = %q, want %q", got, "first")
	}

	// Snapshot the workspace symlink target BEFORE Run so we can prove
	// Run replaces contents, not location.
	linkBefore, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	// Rewrite the config so the hook now uses a different token. Run
	// re-reads config each call, so this is picked up on the next Run.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'mkdir -p {shared} && printf %s \"$WRK_TEST_TOKEN\" > {shared}/marker'\n"+
			"          env:\n"+
			"            WRK_TEST_TOKEN: second\n",
	)

	if err := Run(repo, "node", Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Variant path unchanged.
	linkAfter, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink after Run: %v", err)
	}
	if linkAfter != linkBefore {
		t.Errorf("symlink target changed: before=%q after=%q", linkBefore, linkAfter)
	}

	// Marker bytes are fresh — the hook re-ran with the new token.
	got, err = os.ReadFile(filepath.Join(sharedPath, "marker"))
	if err != nil {
		t.Fatalf("marker missing after Run: %v", err)
	}
	if string(got) != "second" {
		t.Errorf("marker = %q, want %q — hook did not re-run", got, "second")
	}

	// Bookkeeping siblings must be cleaned up.
	if _, err := os.Lstat(sharedPath + ".wrk-provisioning"); !os.IsNotExist(err) {
		t.Errorf("provisioning scratch survived: err=%v", err)
	}
	if _, err := os.Lstat(sharedPath + ".wrk-deleting"); !os.IsNotExist(err) {
		t.Errorf("deleting sibling survived: err=%v", err)
	}
}

// TestRunRefusesUnknownResource pins the "not configured" refusal:
// Run against a resource name that does not appear in .wrk.yml MUST
// surface an error whose message names the unknown resource so the
// operator can grep it.
func TestRunRefusesUnknownResource(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)

	err := Run(repo, "missing", Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected Run to refuse unknown resource, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("expected 'not configured' in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to name resource, got %v", err)
	}
}

// TestRunRefusesResourceWithoutHook pins the "no initialize hook"
// refusal: Run has nothing to do for a resource with no initialize
// commands, and silently no-op'ing would confuse users who fixed a
// hook and are watching for the retry.
func TestRunRefusesResourceWithoutHook(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)

	err := Run(repo, "env", Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected Run to refuse resource without hook, got nil")
	}
	if !strings.Contains(err.Error(), "no initialize hook") {
		t.Errorf("expected 'no initialize hook' in error, got %v", err)
	}
}

// TestRunRefusesDetachedResource pins the detach guard: Run against
// a resource the workspace has detached MUST refuse (swapping the
// shared variant would have no effect on the workspace's independent
// copy) and MUST suggest `wrk relink` in the error so the user has a
// clear next step.
func TestRunRefusesDetachedResource(t *testing.T) {
	repo, _, storage := linkWithHook(t, "seed")

	if err := Detach(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	err := Run(repo, "node", Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected Run to refuse detached resource, got nil")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("expected 'detached' in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "wrk relink") {
		t.Errorf("expected 'wrk relink' guidance in error, got %v", err)
	}
}

// TestRunFailedHookLeavesOldVariantIntact pins the atomicity
// contract: when a Run's hook exits non-zero, the shared variant is
// unchanged and the workspace symlink still points at it. The user
// sees an error, but the resource remains in a valid state — no
// half-populated variant, no dangling symlink, no leftover siblings.
func TestRunFailedHookLeavesOldVariantIntact(t *testing.T) {
	repo, sharedPath, storage := linkWithHook(t, "seed")

	// Snapshot the variant contents and symlink target BEFORE the
	// failing Run.
	linkBefore, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(sharedPath, "marker"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	// Rewrite the config so the hook now exits non-zero AFTER writing
	// to scratch (proves the scratch is cleaned even when the hook
	// touched it).
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'mkdir -p {shared} && exit 9'\n",
	)

	err = Run(repo, "node", Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected Run to surface hook failure, got nil")
	}
	if !strings.Contains(err.Error(), "hook command failed") {
		t.Errorf("expected 'hook command failed', got %v", err)
	}

	// Variant contents unchanged.
	after, err := os.ReadFile(filepath.Join(sharedPath, "marker"))
	if err != nil {
		t.Fatalf("marker gone after failed Run: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("variant contents mutated by failed Run: got %q, want %q", after, before)
	}

	// Workspace symlink unchanged and not dangling.
	linkAfter, err := os.Readlink(filepath.Join(repo.Root, "node_modules"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if linkAfter != linkBefore {
		t.Errorf("symlink target changed: before=%q after=%q", linkBefore, linkAfter)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "node_modules")); err != nil {
		t.Errorf("workspace symlink is dangling after failed Run: %v", err)
	}

	// No leftover bookkeeping siblings.
	if _, err := os.Lstat(sharedPath + ".wrk-provisioning"); !os.IsNotExist(err) {
		t.Errorf("provisioning scratch survived: err=%v", err)
	}
	if _, err := os.Lstat(sharedPath + ".wrk-deleting"); !os.IsNotExist(err) {
		t.Errorf("deleting sibling survived: err=%v", err)
	}
}

// TestRunNilRepoErrors pins the nil-guard: Run against a nil
// Repository surfaces a clean error instead of panicking on nil
// deref, so callers that forgot to pass the repo see an actionable
// message.
func TestRunNilRepoErrors(t *testing.T) {
	err := Run(nil, "anything", Options{Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected error for nil repo, got nil")
	}
}

// TestRunDryRunPrintsPlanNoExecution pins the --dry-run contract:
// the plan describes the re-run action so the user can preview it,
// but the shared variant contents are not touched.
func TestRunDryRunPrintsPlanNoExecution(t *testing.T) {
	repo, sharedPath, storage := linkWithHook(t, "seed")

	before, err := os.ReadFile(filepath.Join(sharedPath, "marker"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	var out bytes.Buffer
	if err := Run(repo, "node", Options{
		StorageRoot: storage,
		DryRun:      true,
		Stdout:      &out,
	}); err != nil {
		t.Fatalf("Run(dry-run): %v", err)
	}

	printed := out.String()
	if !strings.Contains(printed, "re-run initialize hook for node") {
		t.Errorf("dry-run plan missing action description:\n%s", printed)
	}

	// Variant contents untouched.
	after, err := os.ReadFile(filepath.Join(sharedPath, "marker"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("dry-run mutated variant: got %q, want %q", after, before)
	}
}
