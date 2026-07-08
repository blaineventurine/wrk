package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestRelinkDiscardsIndependentCopies pins the reconciliation
// contract: after Detach materialized an independent workspace copy,
// Relink discards that copy and re-establishes the symlink to shared
// storage. Any local modifications the user made post-Detach are gone
// — that's the whole point of the destructive contract.
func TestRelinkDiscardsIndependentCopies(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "shared\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Divergence: mutate the independent copy so the Relink discard is
	// observable — after Relink, the file MUST resolve back to the
	// original shared bytes.
	writeFile(t, filepath.Join(repo.Root, ".env"), "divergent\n")

	if err := Relink(repo, opts); err != nil {
		t.Fatalf("Relink: %v", err)
	}

	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("workspace .env not a symlink after Relink; mode=%v", info.Mode())
	}
	got, err := os.ReadFile(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("read via symlink: %v", err)
	}
	if string(got) != "shared\n" {
		t.Errorf("after Relink content = %q, want %q (divergent local copy should be gone)",
			got, "shared\n")
	}
}

// TestRelinkClearsDetachRegistry pins that a successful Relink removes
// the workspace's detach record; otherwise `wrk status` would keep
// reporting "detached" long after the user reconciled with shared
// storage.
func TestRelinkClearsDetachRegistry(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if entry := readRegistry(t, repo)[repo.Root]; len(entry) == 0 {
		t.Fatalf("Detach didn't seed the registry — test premise broken")
	}

	if err := Relink(repo, opts); err != nil {
		t.Fatalf("Relink: %v", err)
	}

	reg := readRegistry(t, repo)
	if entry, ok := reg[repo.Root]; ok {
		t.Errorf("registry entry survived Relink: %v", entry)
	}
}

// TestRelinkDryRunSkipsMutation pins the --dry-run contract for
// Relink: neither the workspace filesystem nor the on-disk registry
// are touched. The plan is still printed so users can preview what
// would happen.
func TestRelinkDryRunSkipsMutation(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Snapshot: workspace .env is a real file, registry has an entry.
	beforeInfo, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("baseline lstat: %v", err)
	}
	beforeEntry := readRegistry(t, repo)[repo.Root]
	if len(beforeEntry) == 0 {
		t.Fatalf("baseline registry empty — test premise broken")
	}

	dryOpts := opts
	dryOpts.DryRun = true
	dryOpts.Stdout = &bytes.Buffer{}
	if err := Relink(repo, dryOpts); err != nil {
		t.Fatalf("Relink(dry-run): %v", err)
	}

	afterInfo, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("post lstat: %v", err)
	}
	if afterInfo.Mode() != beforeInfo.Mode() {
		t.Errorf("workspace .env mode changed by dry-run: before=%v, after=%v",
			beforeInfo.Mode(), afterInfo.Mode())
	}

	afterEntry := readRegistry(t, repo)[repo.Root]
	if len(afterEntry) != len(beforeEntry) {
		t.Errorf("dry-run mutated registry: before=%v, after=%v",
			beforeEntry, afterEntry)
	}
}
