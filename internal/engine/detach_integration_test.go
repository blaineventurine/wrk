package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestDetachReplacesSymlinksWithLocalCopies pins the whole point of
// Detach: the workspace path that Link left as a symlink becomes a
// real file/dir independent of shared storage, while shared storage
// itself is untouched.
func TestDetachReplacesSymlinksWithLocalCopies(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Snapshot shared bytes so we can prove Detach doesn't touch them.
	sharedAbs, err := filepath.Abs(filepath.Join(storage, repo.RepositoryID, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	sharedBytes, err := os.ReadFile(sharedAbs)
	if err != nil {
		t.Fatalf("read shared before Detach: %v", err)
	}

	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Workspace path is now a real file, NOT a symlink.
	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat after Detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("workspace .env is still a symlink after Detach; mode=%v", info.Mode())
	}
	got, err := os.ReadFile(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("read workspace copy: %v", err)
	}
	if string(got) != "seed\n" {
		t.Errorf("workspace copy content = %q, want %q", got, "seed\n")
	}

	// Shared side unchanged.
	after, err := os.ReadFile(sharedAbs)
	if err != nil {
		t.Fatalf("read shared after Detach: %v", err)
	}
	if !bytes.Equal(after, sharedBytes) {
		t.Errorf("shared bytes changed by Detach: before=%q, after=%q", sharedBytes, after)
	}
}

// TestDetachRecordsPathsInRegistry pins that a successful Detach
// appends the workspace-relative paths it materialized to the on-disk
// registry, so a later Status call can distinguish "detached on
// purpose" from a coincidental conflict.
func TestDetachRecordsPathsInRegistry(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"  - name: cfg\n"+
			"    path: cfg.toml\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "a\n")
	writeFile(t, filepath.Join(repo.Root, "cfg.toml"), "b\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	got := sortedRegistryEntry(readRegistry(t, repo), repo.Root)
	want := []string{".env", "cfg.toml"}
	if len(got) != len(want) {
		t.Fatalf("registry entry = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("registry entry[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestDetachIsIdempotent covers the C1 regression: after the first
// Detach the workspace has no symlinks left, so the second Detach
// builds an empty plan. That empty plan must NOT wipe the registry
// entry recorded by the first run.
func TestDetachIsIdempotent(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach #1: %v", err)
	}
	firstEntry := sortedRegistryEntry(readRegistry(t, repo), repo.Root)

	// Second Detach: nothing to do; must not lose the entry.
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach #2: %v", err)
	}
	secondEntry := sortedRegistryEntry(readRegistry(t, repo), repo.Root)

	if len(firstEntry) == 0 {
		t.Fatalf("first Detach failed to record anything")
	}
	if len(secondEntry) != len(firstEntry) {
		t.Fatalf("registry lost entries on idempotent Detach: first=%v, second=%v",
			firstEntry, secondEntry)
	}
	for i, v := range firstEntry {
		if secondEntry[i] != v {
			t.Errorf("entry[%d]: first=%q, second=%q", i, v, secondEntry[i])
		}
	}
}

// TestDetachIncrementalAppendsRegistry pins the registry's union
// semantics across two Detach calls that each detach a different
// resource. The setup narrows the config between calls so each Detach
// touches only one live symlink; the registry MUST accumulate both.
func TestDetachIncrementalAppendsRegistry(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// Two resources both linked from the start.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"  - name: cfg\n"+
			"    path: cfg.toml\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "a\n")
	writeFile(t, filepath.Join(repo.Root, "cfg.toml"), "b\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Narrow config to env only, then Detach. cfg.toml stays as a
	// symlink because it's no longer in the plan.
	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach #1: %v", err)
	}

	firstEntry := sortedRegistryEntry(readRegistry(t, repo), repo.Root)
	if len(firstEntry) != 1 || firstEntry[0] != ".env" {
		t.Fatalf("first Detach registry = %v, want [.env]", firstEntry)
	}

	// Restore both resources. Now .env is a real file (detached) and
	// cfg.toml is still a symlink; the second Detach plans only the
	// cfg.toml symlink but the registry union MUST retain .env.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n"+
			"  - name: cfg\n"+
			"    path: cfg.toml\n",
	)
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach #2: %v", err)
	}

	got := sortedRegistryEntry(readRegistry(t, repo), repo.Root)
	want := []string{".env", "cfg.toml"}
	if len(got) != len(want) {
		t.Fatalf("registry after second Detach = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("registry[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestDetachDryRunDoesNotWriteRegistry pins the --dry-run contract:
// planning is printed, but neither the filesystem nor the on-disk
// registry are mutated.
func TestDetachDryRunDoesNotWriteRegistry(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Baseline: registry is empty for repo.Root.
	if entry := readRegistry(t, repo)[repo.Root]; entry != nil {
		t.Fatalf("baseline registry non-empty: %v", entry)
	}

	dryOpts := opts
	dryOpts.DryRun = true
	dryOpts.Stdout = &bytes.Buffer{}
	if err := Detach(repo, dryOpts); err != nil {
		t.Fatalf("Detach(dry-run): %v", err)
	}

	// Registry MUST still be empty.
	if entry := readRegistry(t, repo)[repo.Root]; entry != nil {
		t.Errorf("dry-run Detach wrote to registry: %v", entry)
	}

	// And the workspace symlink still exists.
	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat after dry-run: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("dry-run Detach mutated filesystem; workspace .env is no longer a symlink")
	}
}
