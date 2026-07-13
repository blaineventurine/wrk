package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gofrs/flock"
)

// TestGCOrphanDetectClassification pins the classification contract of
// detectOrphanedStorage against an on-disk storage fixture:
//
//   - a configured resource path is protected EXACT (kept, interior
//     never entered — variants belong to the variant sweep),
//   - proper ancestors of a protected path are kept but DESCENDED, so
//     a stray sibling inside an intermediate dir is orphaned on its
//     own (client/cache next to client/node_modules),
//   - an unconfigured top-level tree is orphaned at its ROOT only
//     (nested content is inside the orphan, not a second orphan),
//   - a glob resource protects its static prefix EXACT (`packages`
//     for `packages/*/node_modules`), so nothing under packages/ is
//     ever orphaned — not even a non-matching stray,
//   - bookkeeping entries are never orphans (they belong to the
//     bookkeeping sweep),
//   - results are sorted by RelPath,
//   - a repo whose storage subtree does not exist yields no orphans
//     and no notes.
func TestGCOrphanDetectClassification(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)

	cases := []struct {
		name string
		// config is written verbatim to <repo>/.wrk.yml.
		config string
		// dirs/files are storage-repo-relative slash paths created
		// under <storage>/<repo-id>/ before the sweep.
		dirs  []string
		files map[string]string
		// noRepoDir suppresses creation of <storage>/<repo-id>/
		// entirely (dirs/files must be empty).
		noRepoDir bool
		// want is the exact ordered list of orphan RelPaths.
		want []string
	}{
		{
			name:   "configured resource kept",
			config: "resources:\n  - name: vendor\n    path: \"vendor\"\n",
			dirs:   []string{"vendor/pkg-a"},
			want:   nil,
		},
		{
			name:   "variant subdirs under configured path kept",
			config: "resources:\n  - name: node\n    path: \"client/node_modules\"\n",
			dirs: []string{
				"client/node_modules/aaaa1111",
				"client/node_modules/bbbb2222",
			},
			want: nil,
		},
		{
			name:   "stray sibling under intermediate ancestor orphaned",
			config: "resources:\n  - name: node\n    path: \"client/node_modules\"\n",
			dirs: []string{
				"client/node_modules/aaaa1111",
				"client/cache",
			},
			want: []string{"client/cache"},
		},
		{
			name:   "unconfigured top-level tree orphaned at its root",
			config: "resources:\n  - name: vendor\n    path: \"vendor\"\n",
			dirs: []string{
				"vendor/pkg-a",
				"oldstuff/nested/deep",
			},
			want: []string{"oldstuff"},
		},
		{
			name:   "glob static prefix protected exact without matches",
			config: "resources:\n  - name: pkgs\n    path: \"packages/*/node_modules\"\n",
			dirs:   []string{"packages/stray"},
			want:   nil,
		},
		{
			name:   "glob static prefix protected exact with matches",
			config: "resources:\n  - name: pkgs\n    path: \"packages/*/node_modules\"\n",
			dirs: []string{
				"packages/app1/node_modules/v1",
				"packages/stray",
			},
			want: nil,
		},
		{
			name:   "bookkeeping entries never orphaned",
			config: "resources:\n  - name: vendor\n    path: \"vendor\"\n",
			dirs: []string{
				"vendor",
				"gone.wrk-deleting/junk",
			},
			files: map[string]string{
				"stale.wrk-lock":   "",
				"junk.wrk-tmp":     "",
				"old.wrk-backup":   "",
				"x.wrk-forgetting": "",
			},
			want: nil,
		},
		{
			name:   "stray plain file orphaned",
			config: "resources:\n  - name: vendor\n    path: \"vendor\"\n",
			files:  map[string]string{"notes.txt": "leftover"},
			want:   []string{"notes.txt"},
		},
		{
			name:   "multiple orphans sorted by relpath",
			config: "resources:\n  - name: vendor\n    path: \"vendor\"\n",
			dirs: []string{
				"zebra/z",
				"alpha/a",
				"vendor/pkg",
			},
			want: []string{"alpha", "zebra"},
		},
		{
			name:      "missing storage subtree yields nothing",
			config:    "resources:\n  - name: vendor\n    path: \"vendor\"\n",
			noRepoDir: true,
			want:      nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, filepath.Join(repo.Root, ".wrk.yml"), tc.config)

			storage := storageOutside(t)
			storageRepo := filepath.Join(storage, repo.RepositoryID)
			if !tc.noRepoDir {
				if err := os.MkdirAll(storageRepo, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, d := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(storageRepo, filepath.FromSlash(d)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for f, content := range tc.files {
				writeFile(t, filepath.Join(storageRepo, filepath.FromSlash(f)), content)
			}

			orphans, notes, err := detectOrphanedStorage(
				repo, Options{StorageRoot: storage}, []string{repo.Root})
			if err != nil {
				t.Fatalf("detectOrphanedStorage: %v", err)
			}
			if len(notes) != 0 {
				t.Fatalf("notes = %v, want none", notes)
			}

			got := make([]string, 0, len(orphans))
			for _, o := range orphans {
				got = append(got, o.RelPath)
			}
			if !slices.Equal(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
				t.Fatalf("orphan RelPaths = %v, want %v", got, tc.want)
			}
			for _, o := range orphans {
				wantAbs := filepath.Join(storageRepo, filepath.FromSlash(o.RelPath))
				if o.StoragePath != wantAbs {
					t.Errorf("orphan %s StoragePath = %q, want %q",
						o.RelPath, o.StoragePath, wantAbs)
				}
			}
		})
	}
}

// TestGCOrphanDetectSizeIsTreeTotal pins that an orphan's Size is the
// byte total of the regular files under it (the number the plan feeds
// into TotalBytesFreed and the JSON sizeBytes field).
func TestGCOrphanDetectSizeIsTreeTotal(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: vendor\n    path: \"vendor\"\n",
	})
	storage := storageOutside(t)
	storageRepo := filepath.Join(storage, repo.RepositoryID)

	writeFile(t, filepath.Join(storageRepo, "oldstuff", "blob"), strings.Repeat("a", 2048))
	writeFile(t, filepath.Join(storageRepo, "oldstuff", "sub", "more"), strings.Repeat("b", 100))

	orphans, notes, err := detectOrphanedStorage(
		repo, Options{StorageRoot: storage}, []string{repo.Root})
	if err != nil || len(notes) != 0 {
		t.Fatalf("detectOrphanedStorage: err=%v notes=%v", err, notes)
	}
	if len(orphans) != 1 || orphans[0].RelPath != "oldstuff" {
		t.Fatalf("orphans = %+v, want exactly [oldstuff]", orphans)
	}
	if orphans[0].Size != 2148 {
		t.Errorf("Size = %d, want 2148 (2048+100)", orphans[0].Size)
	}
}

// TestGCOrphanDetectIsolationRegistryPin pins the isolation-pin rule:
// a registry entry whose storage path sits under an UNCONFIGURED
// relpath keeps that path AND its ancestors alive (isolated content is
// not reproducible), while unconfigured siblings inside the same
// subtree are still orphaned.
func TestGCOrphanDetectIsolationRegistryPin(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: vendor\n    path: \"vendor\"\n",
	})
	storage := storageOutside(t)
	storageRepo := filepath.Join(storage, repo.RepositoryID)

	// "oldres" is NOT in the config; only the registry pin protects
	// the isolated variant beneath it.
	isolated := filepath.Join(storageRepo, "oldres", "isolated-deadbeef")
	writeFile(t, filepath.Join(isolated, "data"), "private state")
	if err := os.MkdirAll(filepath.Join(storageRepo, "oldres", "stray"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := recordIsolation(repo, repo.Root, "oldres", isolated); err != nil {
		t.Fatalf("recordIsolation: %v", err)
	}

	orphans, notes, err := detectOrphanedStorage(
		repo, Options{StorageRoot: storage}, []string{repo.Root})
	if err != nil || len(notes) != 0 {
		t.Fatalf("detectOrphanedStorage: err=%v notes=%v", err, notes)
	}

	got := make([]string, 0, len(orphans))
	for _, o := range orphans {
		got = append(got, o.RelPath)
	}
	if !slices.Equal(got, []string{"oldres/stray"}) {
		t.Fatalf("orphans = %v, want [oldres/stray] (isolated variant and its ancestors pinned)", got)
	}
}

// TestGCOrphanDetectConfigFailureNote pins the fail-safe: a workspace
// whose config cannot be loaded aborts the sweep with a note naming
// that root — zero orphans, nil error. Deleting storage from a partial
// view of the configs is how data gets lost.
func TestGCOrphanDetectConfigFailureNote(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: vendor\n    path: \"vendor\"\n",
	})
	storage := storageOutside(t)
	storageRepo := filepath.Join(storage, repo.RepositoryID)

	// A would-be orphan proves the sweep really was skipped, not
	// merely empty.
	if err := os.MkdirAll(filepath.Join(storageRepo, "oldstuff"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.Root, ".wrk.yml"), "resources: [\n")

	orphans, notes, err := detectOrphanedStorage(
		repo, Options{StorageRoot: storage}, []string{repo.Root})
	if err != nil {
		t.Fatalf("detectOrphanedStorage: %v (want note, not error)", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("orphans = %+v, want none on a skipped sweep", orphans)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want exactly one", notes)
	}
	if !strings.Contains(notes[0], "skipped") || !strings.Contains(notes[0], repo.Root) {
		t.Errorf("note %q should mention the skip and name the root %q", notes[0], repo.Root)
	}
}

// TestGCOrphanPlanNoteOnBrokenSecondaryConfig pins the same fail-safe
// end-to-end through BuildGCPlan: invalid YAML in a SECONDARY
// workspace (the primary's config is fine) must skip the sweep with a
// note naming that workspace, leave OrphanedStorage empty, and NOT
// count as work (HasNothing stays true).
func TestGCOrphanPlanNoteOnBrokenSecondaryConfig(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: \".env\"\n",
	})
	secondary, _ := addGitWorktree(t, repo, "wrk-orphan-broken")
	writeFile(t, filepath.Join(secondary, ".wrk.yml"), "resources: [\n")

	storage := storageOutside(t)
	storageRepo := filepath.Join(storage, repo.RepositoryID)
	if err := os.MkdirAll(filepath.Join(storageRepo, "oldstuff"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.OrphanedStorage) != 0 {
		t.Fatalf("OrphanedStorage = %+v, want none (sweep must be skipped)", plan.OrphanedStorage)
	}
	if len(plan.OrphanedStorageNotes) != 1 {
		t.Fatalf("OrphanedStorageNotes = %v, want exactly one", plan.OrphanedStorageNotes)
	}
	note := plan.OrphanedStorageNotes[0]
	if !strings.Contains(note, "skipped") || !strings.Contains(note, secondary) {
		t.Errorf("note %q should mention the skip and name the broken root %q", note, secondary)
	}
	if !plan.HasNothing() {
		t.Errorf("HasNothing = false, want true (a note alone is not work): %+v", plan)
	}
}

// TestGCOrphanExecuteDeletesTreeAndLock is the happy path end-to-end:
// BuildGCPlan flags the unconfigured subtree (and counts its bytes in
// TotalBytesFreed), ExecuteGC removes the tree plus its transient
// .wrk-lock and .wrk-deleting bookkeeping, the configured resource's
// storage survives, and a second plan comes back empty.
func TestGCOrphanExecuteDeletesTreeAndLock(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: \".env\"\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	storageRepo := filepath.Join(storage, repo.RepositoryID)
	orphanPath := filepath.Join(storageRepo, "oldstuff")
	writeFile(t, filepath.Join(orphanPath, "blob"), strings.Repeat("x", 1234))

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.OrphanedStorage) != 1 {
		t.Fatalf("OrphanedStorage = %+v, want exactly one entry", plan.OrphanedStorage)
	}
	tree := plan.OrphanedStorage[0]
	if tree.RelPath != "oldstuff" || tree.StoragePath != orphanPath {
		t.Fatalf("orphan = %+v, want RelPath=oldstuff StoragePath=%s", tree, orphanPath)
	}
	if tree.Size != 1234 {
		t.Errorf("orphan Size = %d, want 1234", tree.Size)
	}
	if len(plan.DeleteVariants) != 0 {
		t.Fatalf("DeleteVariants = %+v, want none (env is pinned)", plan.DeleteVariants)
	}
	if plan.TotalBytesFreed != 1234 {
		t.Errorf("TotalBytesFreed = %d, want 1234 (orphan bytes counted)", plan.TotalBytesFreed)
	}
	if plan.HasNothing() {
		t.Fatal("HasNothing = true, want false (an orphaned subtree is work)")
	}

	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	for _, gone := range []string{orphanPath, orphanPath + ".wrk-lock", orphanPath + ".wrk-deleting"} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be gone after ExecuteGC, got err=%v", gone, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(storageRepo, ".env")); err != nil {
		t.Errorf("configured resource storage must survive: %v", err)
	}

	plan2, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("second BuildGCPlan: %v", err)
	}
	if !plan2.HasNothing() {
		t.Errorf("second plan not empty: %+v", plan2)
	}
}

// TestGCOrphanDeleteMarkerRecovery pins the crash-recovery branch of
// deleteOrphanedTree: a pre-existing <tree>.wrk-deleting marker means
// a prior delete was interrupted mid-RemoveAll. The call must finish
// removing the marker and return WITHOUT touching the main path — the
// tree at the real path is re-evaluated by the next gc, not swept
// blind on stale plan data.
func TestGCOrphanDeleteMarkerRecovery(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: \".env\"\n",
	})
	storage := storageOutside(t)
	storageRepo := filepath.Join(storage, repo.RepositoryID)

	orphanPath := filepath.Join(storageRepo, "oldstuff")
	writeFile(t, filepath.Join(orphanPath, "data"), "recreated content")
	writeFile(t, filepath.Join(orphanPath+".wrk-deleting", "stale"), "crashed remnant")

	var errs []error
	var buf bytes.Buffer
	deleteOrphanedTree(repo,
		orphanedTree{RelPath: "oldstuff", StoragePath: orphanPath},
		Options{StorageRoot: storage, Stdout: &buf},
		func(err error) { errs = append(errs, err) })

	if len(errs) != 0 {
		t.Fatalf("recorded errors: %v", errs)
	}
	if _, err := os.Lstat(orphanPath + ".wrk-deleting"); !os.IsNotExist(err) {
		t.Errorf("stale .wrk-deleting marker should be removed, got err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(orphanPath, "data")); err != nil {
		t.Errorf("main path must be untouched by marker recovery: %v", err)
	}
	if s := buf.String(); strings.Contains(s, "skipping") {
		t.Errorf("no skip message expected, got %q", s)
	}
}

// TestGCOrphanDeleteHeldLock pins the concurrency guard: when another
// process holds <tree>.wrk-lock, deleteOrphanedTree keeps the tree and
// says so on options.Stdout instead of tearing data out from under the
// peer.
func TestGCOrphanDeleteHeldLock(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: \".env\"\n",
	})
	storage := storageOutside(t)
	storageRepo := filepath.Join(storage, repo.RepositoryID)

	orphanPath := filepath.Join(storageRepo, "oldstuff")
	writeFile(t, filepath.Join(orphanPath, "data"), "peer is using this")

	l := flock.New(orphanPath + ".wrk-lock")
	ok, err := l.TryLock()
	if err != nil || !ok {
		t.Fatalf("could not hold lock: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = l.Unlock() })

	var errs []error
	var buf bytes.Buffer
	deleteOrphanedTree(repo,
		orphanedTree{RelPath: "oldstuff", StoragePath: orphanPath},
		Options{StorageRoot: storage, Stdout: &buf},
		func(err error) { errs = append(errs, err) })

	if len(errs) != 0 {
		t.Fatalf("held lock must be a skip, not an error: %v", errs)
	}
	if _, err := os.Lstat(filepath.Join(orphanPath, "data")); err != nil {
		t.Errorf("tree must survive a held lock: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "lock held") || !strings.Contains(out, orphanPath) {
		t.Errorf("stdout %q should mention the held lock and the path", out)
	}
}

// TestGCOrphanExecuteSkipsReclaimedTree pins the execute-time
// re-check: the plan aged at the confirm prompt, and a config edit
// re-adding the resource re-claims the subtree. ExecuteGC must keep
// the tree and report the skip on options.Stdout.
func TestGCOrphanExecuteSkipsReclaimedTree(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: \".env\"\n",
	})
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	storageRepo := filepath.Join(storage, repo.RepositoryID)
	orphanPath := filepath.Join(storageRepo, "oldres")
	writeFile(t, filepath.Join(orphanPath, "data"), "still wanted")

	plan, err := BuildGCPlan(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("BuildGCPlan: %v", err)
	}
	if len(plan.OrphanedStorage) != 1 || plan.OrphanedStorage[0].RelPath != "oldres" {
		t.Fatalf("OrphanedStorage = %+v, want exactly [oldres]", plan.OrphanedStorage)
	}

	// Re-claim between plan and execute: the user adds the resource
	// back to .wrk.yml.
	writeFile(t, filepath.Join(repo.Root, ".wrk.yml"),
		"resources:\n"+
			"  - name: env\n    path: \".env\"\n"+
			"  - name: old\n    path: \"oldres\"\n")

	var buf bytes.Buffer
	if err := ExecuteGC(repo, plan, Options{StorageRoot: storage, Stdout: &buf}); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}

	if !strings.Contains(buf.String(), "re-claimed") {
		t.Errorf("stdout %q should mention the re-claimed skip", buf.String())
	}
	if _, err := os.Lstat(filepath.Join(orphanPath, "data")); err != nil {
		t.Errorf("re-claimed tree must survive ExecuteGC: %v", err)
	}
}

// TestGCOrphanHasNothingCounting pins the HasNothing arithmetic:
// an orphaned subtree is work; a note about a skipped sweep is not.
func TestGCOrphanHasNothingCounting(t *testing.T) {
	withOrphan := GCPlan{OrphanedStorage: []orphanedTree{{RelPath: "x", StoragePath: "/s/x"}}}
	if withOrphan.HasNothing() {
		t.Error("plan with OrphanedStorage: HasNothing = true, want false")
	}

	notesOnly := GCPlan{OrphanedStorageNotes: []string{"orphaned-storage sweep skipped: boom"}}
	if !notesOnly.HasNothing() {
		t.Error("plan with only OrphanedStorageNotes: HasNothing = false, want true")
	}
}

// TestGCOrphanPrintSectionAndTotals pins the human rendering: the
// section header with count, one "✗ <relpath>\t<size>" line per tree,
// "! " note lines, and both the reclaimed bytes and the orphan count
// in the totals line.
func TestGCOrphanPrintSectionAndTotals(t *testing.T) {
	plan := GCPlan{
		OrphanedStorage: []orphanedTree{
			{RelPath: "client/cache", StoragePath: "/s/client/cache", Size: 2048},
			{RelPath: "oldstuff", StoragePath: "/s/oldstuff", Size: 512},
		},
		OrphanedStorageNotes: []string{"heads-up note"},
		TotalBytesFreed:      2560,
	}

	var buf bytes.Buffer
	PrintGCPlan(&buf, plan)
	out := buf.String()

	for _, want := range []string{
		"Orphaned storage (2, not referenced by any live workspace's config):",
		"✗ client/cache\t2 KB",
		"✗ oldstuff\t512 B",
		"! heads-up note",
		"2 orphaned subtree",
		"2.5 KB reclaimed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Nothing to do.") {
		t.Errorf("plan with orphans must not print the empty banner:\n%s", out)
	}
}

// TestGCOrphanPrintNothingToDoWithNotes pins the early-return branch:
// an otherwise-empty plan still surfaces a skipped-sweep note after
// "Nothing to do." — silence would read as "storage fully checked".
func TestGCOrphanPrintNothingToDoWithNotes(t *testing.T) {
	plan := GCPlan{
		OrphanedStorageNotes: []string{"orphaned-storage sweep skipped: config unreadable in /w (boom)"},
	}

	var buf bytes.Buffer
	PrintGCPlan(&buf, plan)

	want := "Nothing to do.\n! orphaned-storage sweep skipped: config unreadable in /w (boom)\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

// TestGCOrphanJSONProjection pins the machine-readable shape: each
// plan.orphanedStorage entry carries path/storagePath/sizeBytes, and
// plan.orphanedStorageNotes carries the note strings.
func TestGCOrphanJSONProjection(t *testing.T) {
	data, err := MarshalGCJSON(GCJSONInput{
		Plan: GCPlan{
			OrphanedStorage: []orphanedTree{
				{RelPath: "client/cache", StoragePath: "/s/client/cache", Size: 2048},
			},
			OrphanedStorageNotes: []string{"n1"},
		},
	})
	if err != nil {
		t.Fatalf("MarshalGCJSON: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	plan, ok := m["plan"].(map[string]any)
	if !ok {
		t.Fatalf("plan key missing or wrong type in %s", data)
	}

	arr, ok := plan["orphanedStorage"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("orphanedStorage = %v, want one-element array", plan["orphanedStorage"])
	}
	entry, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("orphanedStorage[0] = %v, want object", arr[0])
	}
	if entry["path"] != "client/cache" {
		t.Errorf("path = %v, want client/cache", entry["path"])
	}
	if entry["storagePath"] != "/s/client/cache" {
		t.Errorf("storagePath = %v, want /s/client/cache", entry["storagePath"])
	}
	if entry["sizeBytes"] != float64(2048) {
		t.Errorf("sizeBytes = %v, want 2048", entry["sizeBytes"])
	}

	notes, ok := plan["orphanedStorageNotes"].([]any)
	if !ok || len(notes) != 1 || notes[0] != "n1" {
		t.Errorf("orphanedStorageNotes = %v, want [n1]", plan["orphanedStorageNotes"])
	}
}

// TestGCOrphanJSONEmptyArraysNotNull pins the never-null contract: an
// empty sweep emits [] for both orphanedStorage and
// orphanedStorageNotes, never JSON null.
func TestGCOrphanJSONEmptyArraysNotNull(t *testing.T) {
	data, err := MarshalGCJSON(GCJSONInput{Plan: GCPlan{}})
	if err != nil {
		t.Fatalf("MarshalGCJSON: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	plan, ok := m["plan"].(map[string]any)
	if !ok {
		t.Fatalf("plan key missing or wrong type in %s", data)
	}

	for _, key := range []string{"orphanedStorage", "orphanedStorageNotes"} {
		value, present := plan[key]
		if !present {
			t.Errorf("%s key absent, want []", key)
			continue
		}
		arr, isArr := value.([]any)
		if !isArr {
			t.Errorf("%s = %v (%T), want [] not null", key, value, value)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("%s = %v, want empty", key, arr)
		}
	}
}
