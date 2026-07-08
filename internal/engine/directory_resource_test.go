package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestDirectoryResourceLinkMovesDirectoryToShared pins that a resource
// whose path is a DIRECTORY (the node_modules/vendor-cache case that
// actually motivates wrk) rides the same Move → Symlink pipeline as a
// single-file resource: workspace-side becomes a symlink, and every
// nested child ends up in shared storage with intact bytes.
//
// The existing link tests only exercise a single-file `.env`, so a
// regression that broke dircopy or the executor's directory branch
// would ship green today.
func TestDirectoryResourceLinkMovesDirectoryToShared(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: node\n    path: node_modules\n",
	)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "a.js"), "console.log('a')\n")
	writeFile(t, filepath.Join(repo.Root, "node_modules", "nested", "b.js"), "console.log('b')\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Workspace-side: node_modules is now a symlink.
	wsPath := filepath.Join(repo.Root, "node_modules")
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat workspace node_modules: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace node_modules is not a symlink; mode=%v", info.Mode())
	}

	// Reading through the symlink returns every original child with
	// the original content — the whole point of the Move.
	gotA, err := os.ReadFile(filepath.Join(wsPath, "a.js"))
	if err != nil {
		t.Fatalf("read through symlink a.js: %v", err)
	}
	if string(gotA) != "console.log('a')\n" {
		t.Errorf("a.js through symlink = %q, want %q", gotA, "console.log('a')\n")
	}
	gotB, err := os.ReadFile(filepath.Join(wsPath, "nested", "b.js"))
	if err != nil {
		t.Fatalf("read through symlink nested/b.js: %v", err)
	}
	if string(gotB) != "console.log('b')\n" {
		t.Errorf("nested/b.js through symlink = %q, want %q", gotB, "console.log('b')\n")
	}

	// Shared side: the underlying directory really exists and holds
	// the same bytes at the same relative paths. Read via os.Lstat
	// so a rogue symlink at the shared side (which would satisfy the
	// through-workspace read above via a chain of links) fails the
	// mode check.
	sharedRoot := filepath.Join(storage, repo.RepositoryID, "node_modules")
	sharedInfo, err := os.Lstat(sharedRoot)
	if err != nil {
		t.Fatalf("lstat shared node_modules: %v", err)
	}
	if sharedInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("shared node_modules is a symlink; want a real directory")
	}
	if !sharedInfo.IsDir() {
		t.Fatalf("shared node_modules is not a directory; mode=%v", sharedInfo.Mode())
	}
	rawA, err := os.ReadFile(filepath.Join(sharedRoot, "a.js"))
	if err != nil {
		t.Fatalf("read shared a.js: %v", err)
	}
	if string(rawA) != "console.log('a')\n" {
		t.Errorf("shared a.js = %q, want %q", rawA, "console.log('a')\n")
	}
	rawB, err := os.ReadFile(filepath.Join(sharedRoot, "nested", "b.js"))
	if err != nil {
		t.Fatalf("read shared nested/b.js: %v", err)
	}
	if string(rawB) != "console.log('b')\n" {
		t.Errorf("shared nested/b.js = %q, want %q", rawB, "console.log('b')\n")
	}
}

// TestDirectoryResourceDetachRestoresRealDirectoryWithAllChildren
// pins that Detach on a directory resource materializes the full
// tree back into the workspace — not just the top-level directory,
// not just a subset of files. A dircopy regression that stopped at
// the first level would satisfy the "workspace is a directory now"
// check while losing nested/b.js; the nested read pins it down.
func TestDirectoryResourceDetachRestoresRealDirectoryWithAllChildren(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: node\n    path: node_modules\n",
	)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "a.js"), "console.log('a')\n")
	writeFile(t, filepath.Join(repo.Root, "node_modules", "nested", "b.js"), "console.log('b')\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Snapshot shared bytes so we can prove Detach doesn't touch them.
	sharedRoot := filepath.Join(storage, repo.RepositoryID, "node_modules")
	sharedABefore, err := os.ReadFile(filepath.Join(sharedRoot, "a.js"))
	if err != nil {
		t.Fatalf("read shared a.js before Detach: %v", err)
	}

	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Workspace side: a real directory, not a symlink.
	wsPath := filepath.Join(repo.Root, "node_modules")
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat workspace node_modules after Detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("workspace node_modules still a symlink after Detach; mode=%v", info.Mode())
	}
	if !info.IsDir() {
		t.Errorf("workspace node_modules is not a directory after Detach; mode=%v", info.Mode())
	}

	// Every original child is present with the original content.
	gotA, err := os.ReadFile(filepath.Join(wsPath, "a.js"))
	if err != nil {
		t.Fatalf("read workspace a.js after Detach: %v", err)
	}
	if string(gotA) != "console.log('a')\n" {
		t.Errorf("workspace a.js after Detach = %q, want %q", gotA, "console.log('a')\n")
	}
	gotB, err := os.ReadFile(filepath.Join(wsPath, "nested", "b.js"))
	if err != nil {
		t.Fatalf("read workspace nested/b.js after Detach: %v", err)
	}
	if string(gotB) != "console.log('b')\n" {
		t.Errorf("workspace nested/b.js after Detach = %q, want %q", gotB, "console.log('b')\n")
	}

	// And the shared side is untouched.
	sharedAAfter, err := os.ReadFile(filepath.Join(sharedRoot, "a.js"))
	if err != nil {
		t.Fatalf("read shared a.js after Detach: %v", err)
	}
	if !bytes.Equal(sharedAAfter, sharedABefore) {
		t.Errorf("shared a.js changed by Detach: before=%q, after=%q",
			sharedABefore, sharedAAfter)
	}
}

// TestDirectoryResourceRelinkDiscardsIndependentDirectory pins the
// destructive contract of Relink for a directory: any files the user
// added to the detached copy vanish, and any files they edited snap
// back to the SHARED bytes. This is the read side of the Move → Symlink
// swap — the symlink now points at shared, so a stat via the workspace
// path resolves to shared for every child.
func TestDirectoryResourceRelinkDiscardsIndependentDirectory(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: node\n    path: node_modules\n",
	)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "a.js"), "console.log('a')\n")
	writeFile(t, filepath.Join(repo.Root, "node_modules", "nested", "b.js"), "console.log('b')\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Divergence: independent copy grows a new file and mutates an
	// existing one. Relink is expected to blow both away.
	wsPath := filepath.Join(repo.Root, "node_modules")
	writeFile(t, filepath.Join(wsPath, "a.js"), "// local edit\n")
	writeFile(t, filepath.Join(wsPath, "added.js"), "// added post-detach\n")

	if err := Relink(repo, opts); err != nil {
		t.Fatalf("Relink: %v", err)
	}

	// Workspace side is a symlink again.
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("lstat after Relink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("workspace node_modules not a symlink after Relink; mode=%v", info.Mode())
	}

	// Added file is gone (visible neither through the symlink nor
	// on the shared side).
	if _, err := os.Stat(filepath.Join(wsPath, "added.js")); !os.IsNotExist(err) {
		t.Errorf("added.js survived Relink through workspace: err=%v", err)
	}
	sharedRoot := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if _, err := os.Stat(filepath.Join(sharedRoot, "added.js")); !os.IsNotExist(err) {
		t.Errorf("added.js leaked into shared storage: err=%v", err)
	}

	// The edited file's read through the workspace symlink returns
	// the SHARED bytes — not the local edit.
	gotA, err := os.ReadFile(filepath.Join(wsPath, "a.js"))
	if err != nil {
		t.Fatalf("read a.js after Relink: %v", err)
	}
	if string(gotA) != "console.log('a')\n" {
		t.Errorf("a.js after Relink = %q, want shared %q", gotA, "console.log('a')\n")
	}

	// Original nested child is still accessible via the symlink.
	gotB, err := os.ReadFile(filepath.Join(wsPath, "nested", "b.js"))
	if err != nil {
		t.Fatalf("read nested/b.js after Relink: %v", err)
	}
	if string(gotB) != "console.log('b')\n" {
		t.Errorf("nested/b.js after Relink = %q, want %q", gotB, "console.log('b')\n")
	}
}
