package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestExternalStorageLinkDetachRelinkCycle pins the full lifecycle
// against production-default storage — an XDG-style tree that lives
// entirely OUTSIDE the workspace root. This is the load-bearing
// regression for the leaf-symlink fix in internal/executor/contain.go:
// before that fix, the very first Detach on such a setup failed
// because the executor resolved the workspace-side symlink through
// its leaf and complained that shared storage "escapes workspace
// root". The whole external-storage flow needs a green
// Link → Detach → Relink round-trip to be considered fixed.
func TestExternalStorageLinkDetachRelinkCycle(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageOutside(t)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	// Guardrail: storage and repo MUST have no common ancestor other
	// than the OS's fs root. Otherwise the "external storage" claim
	// is a lie and every assertion below trivially passes.
	if strings.HasPrefix(storage, repo.Root+string(filepath.Separator)) ||
		strings.HasPrefix(repo.Root, storage+string(filepath.Separator)) {
		t.Fatalf("storage %q and repo %q share an ancestor; storageOutside is not exercising the external-storage regression",
			storage, repo.Root)
	}

	// --- Link ------------------------------------------------------
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	workspaceEnv := filepath.Join(repo.Root, ".env")
	info, err := os.Lstat(workspaceEnv)
	if err != nil {
		t.Fatalf("lstat after Link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace .env is not a symlink after Link; mode=%v", info.Mode())
	}
	target, err := os.Readlink(workspaceEnv)
	if err != nil {
		t.Fatalf("readlink after Link: %v", err)
	}
	if !strings.HasPrefix(target, storage+string(filepath.Separator)) {
		t.Errorf("symlink target %q does not start with external storage prefix %q",
			target, storage+string(filepath.Separator))
	}
	// Target MUST resolve — a dangling symlink after a supposedly
	// successful Link would satisfy the prefix check while breaking
	// the user.
	resolved, err := filepath.EvalSymlinks(workspaceEnv)
	if err != nil {
		t.Fatalf("evalsymlinks after Link: %v", err)
	}
	wantShared := filepath.Join(storage, repo.RepositoryID, ".env")
	if resolved != wantShared {
		t.Errorf("resolved symlink = %q, want %q", resolved, wantShared)
	}
	sharedBytes, err := os.ReadFile(wantShared)
	if err != nil {
		t.Fatalf("read shared after Link: %v", err)
	}
	if string(sharedBytes) != "seed\n" {
		t.Errorf("shared bytes after Link = %q, want %q", sharedBytes, "seed\n")
	}

	// --- Detach ----------------------------------------------------
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	info, err = os.Lstat(workspaceEnv)
	if err != nil {
		t.Fatalf("lstat after Detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("workspace .env still a symlink after Detach; mode=%v", info.Mode())
	}
	if !info.Mode().IsRegular() {
		t.Errorf("workspace .env is not a regular file after Detach; mode=%v", info.Mode())
	}
	got, err := os.ReadFile(workspaceEnv)
	if err != nil {
		t.Fatalf("read workspace .env after Detach: %v", err)
	}
	if string(got) != "seed\n" {
		t.Errorf("workspace .env content after Detach = %q, want %q", got, "seed\n")
	}
	sharedAfterDetach, err := os.ReadFile(wantShared)
	if err != nil {
		t.Fatalf("read shared after Detach: %v", err)
	}
	if !bytes.Equal(sharedAfterDetach, sharedBytes) {
		t.Errorf("shared bytes changed by Detach: before=%q, after=%q",
			sharedBytes, sharedAfterDetach)
	}

	// --- Relink ----------------------------------------------------
	if err := Relink(repo, opts); err != nil {
		t.Fatalf("Relink: %v", err)
	}

	info, err = os.Lstat(workspaceEnv)
	if err != nil {
		t.Fatalf("lstat after Relink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("workspace .env is not a symlink after Relink; mode=%v", info.Mode())
	}
	target, err = os.Readlink(workspaceEnv)
	if err != nil {
		t.Fatalf("readlink after Relink: %v", err)
	}
	if !strings.HasPrefix(target, storage+string(filepath.Separator)) {
		t.Errorf("Relink symlink target %q does not start with external storage prefix %q",
			target, storage+string(filepath.Separator))
	}
}

// TestExternalStorageStatusReports pins that Status correctly reads
// through the linked, external-storage workspace: the derived state
// is `linked` and the reported SharedPath actually lives under the
// external tree. A regression that miscomputed the shared path
// (e.g. by re-rooting it under the repo) would surface here even if
// Link itself still worked, since Status uses the same location.For
// but consumes it for user-facing output.
func TestExternalStorageStatusReports(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageOutside(t)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	report, err := Status(repo, opts)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1: %+v", len(report.Rows), report.Rows)
	}
	row := report.Rows[0]
	if row.State != StateLinked {
		t.Errorf("state = %q, want %q", row.State, StateLinked)
	}
	if row.Resource != "env" {
		t.Errorf("resource = %q, want %q", row.Resource, "env")
	}
	if row.Path != ".env" {
		t.Errorf("path = %q, want %q", row.Path, ".env")
	}
	if !strings.HasPrefix(row.SharedPath, storage+string(filepath.Separator)) {
		t.Errorf("SharedPath %q does not start with external storage prefix %q",
			row.SharedPath, storage+string(filepath.Separator))
	}
	// Pin the exact shared path — Status uses the same location.For,
	// so any drift shows up as a mismatch here rather than as a
	// silent user-visible bug.
	wantShared := filepath.Join(storage, repo.RepositoryID, ".env")
	if row.SharedPath != wantShared {
		t.Errorf("SharedPath = %q, want %q", row.SharedPath, wantShared)
	}
}

// TestExternalStorageResistantToMoveGuard is the tight-scoped C4
// regression: with storage OUTSIDE the workspace, Detach MUST NOT be
// refused by the executor's containment guard. This is exactly the
// scenario production hit — the workspace-side symlink pointed at
// external storage, the guard resolved the leaf, saw the target
// escaped the workspace, and returned an "escapes workspace root"
// error. Its absence here proves the fix stays in.
//
// Signal: a bare success return, plus the specific error substring
// we would have seen if the guard re-regressed. We pin BOTH so a
// generic "some other failure" is not silently accepted.
func TestExternalStorageResistantToMoveGuard(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := storageOutside(t)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// The load-bearing call: Detach must succeed. The old guard
	// would return the string below.
	err := Detach(repo, opts)
	if err != nil {
		if strings.Contains(err.Error(), "escapes workspace root") {
			t.Fatalf("Detach refused external-storage symlink with the regressed guard: %v", err)
		}
		t.Fatalf("Detach: %v", err)
	}

	// And Relink must also survive: it rebuilds the same symlink,
	// and the guard runs again for the trailing Symlink action.
	if err := Relink(repo, opts); err != nil {
		if strings.Contains(err.Error(), "escapes workspace root") {
			t.Fatalf("Relink refused external-storage symlink with the regressed guard: %v", err)
		}
		t.Fatalf("Relink: %v", err)
	}

	// End state: workspace path resolves to a real file under
	// external storage. Prove the round-trip actually happened.
	target, err := os.Readlink(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if !strings.HasPrefix(target, storage+string(filepath.Separator)) {
		t.Errorf("post-Relink symlink target %q not under external storage %q",
			target, storage)
	}
}
