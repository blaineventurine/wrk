package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestLinkHookFailureReturnsError pins the failure contract for
// initialize hooks: a non-zero exit MUST surface to the caller as
// an error whose message names both the failure mode (the wrapped
// "hook command failed") and the exit status the operator needs to
// debug the hook. If runInitialize swallowed the error or reported
// a generic wrapper, the user would see a red Link with no clue
// which hook fired or what it returned.
//
// Filesystem contract: the trailing Symlink action is never
// reached, so the workspace path remains untouched — no dangling
// symlink to a shared-storage path we couldn't fully provision.
func TestLinkHookFailureReturnsError(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'exit 3'\n",
	)
	// Deliberately NO workspace-side node_modules — that forces the
	// hook branch of provisionShared instead of the Move branch.

	err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("Link succeeded despite failing initialize hook")
	}

	// Both substrings are what runInitialize.exec.Cmd.Run wraps:
	// "hook command failed: sh -c exit 3 (in ...): exit status 3".
	// Pin both so a wrapper that dropped the exit status still fails.
	msg := err.Error()
	if !strings.Contains(msg, "hook command failed") {
		t.Errorf("error message %q missing %q", msg, "hook command failed")
	}
	if !strings.Contains(msg, "exit status 3") {
		t.Errorf("error message %q missing %q", msg, "exit status 3")
	}

	// Workspace side: the Symlink action never ran, so node_modules
	// is absent — NOT a symlink.
	if info, err := os.Lstat(filepath.Join(repo.Root, "node_modules")); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("workspace node_modules is a symlink after failed Link; mode=%v", info.Mode())
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("lstat workspace node_modules: %v", err)
	}
}

// TestLinkHookMultiCommandStopsOnFirstFailure pins the fail-fast
// contract across multi-command hooks: given a 3-command hook where
// command #2 fails, command #1's side effect is observable and
// command #3's is NOT. This is the load-bearing invariant runInitialize
// gives the caller — an incidental "continue past failure" refactor
// would poison shared storage with half-baked artifacts.
//
// The markers are written to a t.TempDir() so a filesystem race with
// the shared storage tree (which the failing Link never finishes
// touching) cannot mislead the assertion.
func TestLinkHookMultiCommandStopsOnFirstFailure(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	markerDir := t.TempDir()
	firstMarker := filepath.Join(markerDir, "first-ran")
	thirdMarker := filepath.Join(markerDir, "third-ran")

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'touch "+firstMarker+"'\n"+
			"        - run: sh -c 'exit 1'\n"+
			"        - run: sh -c 'touch "+thirdMarker+"'\n",
	)

	err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("Link succeeded despite failing hook command")
	}
	if !strings.Contains(err.Error(), "hook command failed") {
		t.Errorf("error message %q missing %q", err.Error(), "hook command failed")
	}

	// First marker MUST exist — command #1 ran to completion.
	if _, err := os.Stat(firstMarker); err != nil {
		t.Errorf("first hook command did not run to completion: %v", err)
	}

	// Third marker MUST NOT exist — a regression that continued past
	// the failing #2 would leave it.
	if _, err := os.Stat(thirdMarker); !os.IsNotExist(err) {
		t.Errorf("third hook command ran after failure: err=%v", err)
	}
}

// TestLinkHookFailureRetryableAfterFix pins that a first-run hook
// failure does NOT leave shared storage in a state that blocks the
// retry. Once the user fixes the hook (or the flake self-heals),
// re-running Link MUST succeed and reach the trailing Symlink action.
// If runInitialize left partial state — a stray shared file, a stuck
// lock — the second Link would either skip the (fixed) hook and hand
// out an empty shared dir, or fail with an unhelpful "already
// exists" error.
func TestLinkHookFailureRetryableAfterFix(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// --- Failing config -------------------------------------------
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'exit 1'\n",
	)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err == nil {
		t.Fatal("first Link succeeded despite failing initialize hook")
	}

	// Sanity: nothing landed in shared storage — the failed hook did
	// not partially populate the shared dir.
	sharedPath := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if _, err := os.Stat(sharedPath); err == nil {
		t.Errorf("shared node_modules exists after failed hook: unexpected pre-existing state")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat shared: %v", err)
	}

	// --- Fixed config ---------------------------------------------
	// Replace the hook with one that actually provisions the shared
	// directory. `mkdir -p {shared}` is the canonical minimal hook
	// for a directory resource.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: sh -c 'mkdir -p {shared} && touch {shared}/.fixed'\n",
	)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("second Link (post-fix): %v", err)
	}

	// Workspace side: node_modules is a symlink pointing at the
	// shared path the fixed hook provisioned.
	wsPath := filepath.Join(repo.Root, "node_modules")
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat after retry Link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace node_modules not a symlink after retry; mode=%v", info.Mode())
	}
	target, err := os.Readlink(wsPath)
	if err != nil {
		t.Fatalf("readlink after retry: %v", err)
	}
	if target != sharedPath {
		t.Errorf("symlink target = %q, want %q", target, sharedPath)
	}

	// And the fixed hook's own marker exists under shared — proof
	// the retry actually re-ran the hook rather than short-circuiting
	// on stale state.
	if _, err := os.Stat(filepath.Join(sharedPath, ".fixed")); err != nil {
		t.Errorf("fixed-hook marker missing: %v", err)
	}
}
