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

// TestRelinkNoOpWhenNothingDetached pins that Relink is safe to run
// against an already-linked workspace where the user never called
// Detach: the plan is empty, nothing on disk changes (symlink target
// preserved), the registry stays empty, and no destructive warning is
// emitted (there is nothing destructive to warn about). A regression
// that always planned a Remove+Symlink cycle for "linked" instances
// would silently rewrite the symlink and emit the ⚠ marker even
// though nothing is being discarded.
func TestRelinkNoOpWhenNothingDetached(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Snapshot: symlink identity (inode) and the target string. Relink
	// must not touch either. Checking Lstat inode via os.SameFile
	// catches the subtle regression where Relink always plans
	// Remove+Symlink for a linked instance: the target string would
	// stay the same but the inode would flip.
	envPath := filepath.Join(repo.Root, ".env")
	beforeInfo, err := os.Lstat(envPath)
	if err != nil {
		t.Fatalf("lstat pre-Relink: %v", err)
	}
	before, err := os.Readlink(envPath)
	if err != nil {
		t.Fatalf("readlink pre-Relink: %v", err)
	}
	// Baseline: Link's clearDetached already cleared the registry.
	if entry := readRegistry(t, repo)[repo.Root]; entry != nil {
		t.Fatalf("baseline registry non-empty: %v (Link should have cleared it)", entry)
	}

	// Run Relink WITHOUT a prior Detach. Everything is already linked
	// correctly; there is no independent copy to discard.
	var out bytes.Buffer
	if err := Relink(repo, Options{StorageRoot: storage, Stdout: &out}); err != nil {
		t.Fatalf("Relink no-op: %v", err)
	}

	// Symlink identity preserved: same inode (same on-disk entry, not
	// a re-created one), still a symlink, exact same target string.
	afterInfo, err := os.Lstat(envPath)
	if err != nil {
		t.Fatalf("lstat post-Relink: %v", err)
	}
	if afterInfo.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".env is no longer a symlink after no-op Relink; mode=%v", afterInfo.Mode())
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Errorf("symlink inode changed by no-op Relink (mode before=%v, after=%v) — plan should have been empty",
			beforeInfo.Mode(), afterInfo.Mode())
	}
	after, err := os.Readlink(envPath)
	if err != nil {
		t.Fatalf("readlink post-Relink: %v", err)
	}
	if after != before {
		t.Errorf("symlink target changed by no-op Relink: before=%q, after=%q", before, after)
	}

	// Registry stays empty — clearDetached on an empty entry is a
	// clean no-op, not an error.
	if entry := readRegistry(t, repo)[repo.Root]; entry != nil {
		t.Errorf("no-op Relink populated the registry: %v", entry)
	}

	// Plan output: no destructive markers at all. `⚠` is emitted both
	// on per-action destructive bullets and on the overall destructive
	// warning banner; if either shows up, the printer decided the
	// no-op plan was destructive, which it isn't.
	if bytes.ContainsRune(out.Bytes(), '⚠') {
		t.Errorf("no-op Relink emitted a destructive marker; plan output was:\n%s", out.String())
	}
}
// TestExecuteRelinkClearsDetachedAfterSuccess pins the CLI-facing
// split: ExecuteRelink applies a pre-built plan AND clears the
// detach registry, WITHOUT printing the plan preview (the CLI
// already did via engine.PrintPlan). A regression that put
// printPlan back inside ExecuteRelink would cause `wrk relink` to
// double-print.
func TestExecuteRelinkClearsDetachedAfterSuccess(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "shared\n")

	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// After Detach the registry MUST have an entry — that's the
	// state ExecuteRelink is supposed to clean up.
	if entry := readRegistry(t, repo)[repo.Root]; len(entry) == 0 {
		t.Fatalf("detach registry empty after Detach; nothing for ExecuteRelink to clear")
	}

	plan, err := BuildRelinkPlan(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("BuildRelinkPlan: %v", err)
	}

	var out bytes.Buffer
	if err := ExecuteRelink(repo, plan, Options{StorageRoot: storage, Stdout: &out}); err != nil {
		t.Fatalf("ExecuteRelink: %v", err)
	}

	// Registry cleared.
	if entry := readRegistry(t, repo)[repo.Root]; entry != nil {
		t.Errorf("detach registry still has entry after ExecuteRelink: %v", entry)
	}

	// Workspace .env is a symlink again.
	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("workspace .env not a symlink after ExecuteRelink; mode=%v", info.Mode())
	}

	// And no plan-preview output from Execute — CLI already printed.
	if out.Len() != 0 {
		t.Errorf("ExecuteRelink wrote to stdout (double-print risk):\n%s", out.String())
	}
}
