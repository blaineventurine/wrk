package engine

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blaineventurine/wrk/internal/repository"
)

// newTestCloneWithRemote builds a git repo like newTestRepoWithHead
// and then points `origin` at remoteURL BEFORE detection, so the
// returned Repository derives its RepositoryID from the remote. Two
// repos built with the SAME https URL share a repo-id — the exact
// shape of two independent clones sharing storage.
func newTestCloneWithRemote(
	t *testing.T,
	remoteURL string,
	tracked map[string]string,
) *repository.Repository {
	t.Helper()
	seeded := newTestRepoWithHead(t, tracked)

	cmd := exec.Command("git", "remote", "add", "origin", remoteURL)
	cmd.Dir = seeded.Root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}

	repo, err := repository.Detect(seeded.Root, repository.Auto)
	if err != nil {
		t.Fatalf("repository.Detect: %v", err)
	}
	return repo
}

// twoCloneFixture builds two independent clones sharing one https
// remote (and therefore one repo-id), links a .env resource from
// clone B into the shared storage root, and returns everything the
// cross-clone gc/forget tests need. B's Link is what registers B in
// the clone registry.
func twoCloneFixture(t *testing.T) (cloneA, cloneB *repository.Repository, opts Options) {
	t.Helper()
	const remote = "https://example.com/org/wrk-clones-fixture.git"
	cfg := map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	}
	cloneA = newTestCloneWithRemote(t, remote, cfg)
	cloneB = newTestCloneWithRemote(t, remote, cfg)
	if cloneA.RepositoryID != cloneB.RepositoryID {
		t.Fatalf("fixture: repo-ids diverged: %q vs %q — same-remote clones must share storage",
			cloneA.RepositoryID, cloneB.RepositoryID)
	}

	storage := canonPath(t, t.TempDir())
	opts = Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	writeFile(t, filepath.Join(cloneB.Root, ".env"), "from-clone-b\n")
	if err := Link(cloneB, opts); err != nil {
		t.Fatalf("Link(cloneB): %v", err)
	}
	return cloneA, cloneB, opts
}

// cloneRegistryFile is the documented on-disk location of the clone
// registry: a SIBLING of `<storage>/<repo-id>/`. RepositoryID is
// multi-segment, so the file sits INSIDE the storage tree (e.g.
// `<storage>/example.com/org/x.wrk-clones.json`), never at the
// storage root itself.
func cloneRegistryFile(repo *repository.Repository, opts Options) string {
	return filepath.Join(opts.StorageRoot, repo.RepositoryID+".wrk-clones.json")
}

// captureStderr redirects os.Stderr for the duration of fn and
// returns everything written to it. Mirrors the detach-registry
// corruption test's pattern; callers must not run in parallel.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = origStderr
	buf, _ := io.ReadAll(r)
	return string(buf)
}

// TestRegisterCloneWritesRegistryKeyedByMetadataDir pins the wire
// contract of the registry file: it lives at
// `<storage>/<repo-id>.wrk-clones.json` (a sibling of the repo's
// storage subtree, INSIDE the multi-segment repo-id's parent dirs),
// and maps the clone's canonical metadata dir to an object whose
// `root` field is the clone's canonical primary workspace root.
func TestRegisterCloneWritesRegistryKeyedByMetadataDir(t *testing.T) {
	repo := newTestCloneWithRemote(t, "https://example.com/org/x.git", nil)
	if repo.RepositoryID != "example.com/org/x" {
		t.Fatalf("fixture: RepositoryID = %q, want example.com/org/x", repo.RepositoryID)
	}
	storage := canonPath(t, t.TempDir())
	opts := Options{StorageRoot: storage}

	registerClone(repo, opts)

	// The multi-segment repo-id places the file under
	// <storage>/example.com/org/ — reading it at the composed path
	// proves both the sibling placement and the nested layout.
	data, err := os.ReadFile(cloneRegistryFile(repo, opts))
	if err != nil {
		t.Fatalf("registry file not at <storage>/<repo-id>.wrk-clones.json: %v", err)
	}

	var reg map[string]struct {
		Root      string `json:"root"`
		UpdatedAt string `json:"updatedAt"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("registry is not the documented JSON map: %v\n%s", err, data)
	}
	if len(reg) != 1 {
		t.Fatalf("registry has %d entries, want 1: %s", len(reg), data)
	}
	wantKey, err := filepath.EvalSymlinks(repo.MetadataDir())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reg[wantKey]
	if !ok {
		t.Fatalf("registry key = %v, want canonical metadata dir %q", reg, wantKey)
	}
	if entry.Root != repo.Root {
		t.Errorf("entry.root = %q, want canonical primary root %q", entry.Root, repo.Root)
	}
}

// TestRegisterCloneSkipsRewriteWhenCurrent pins the no-op path: when
// the registry already carries the current entry, a re-registration
// MUST NOT rewrite the file. Detection: pin the file's mtime into the
// past and re-register — a rewrite would bump it to now.
func TestRegisterCloneSkipsRewriteWhenCurrent(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := canonPath(t, t.TempDir())
	opts := Options{StorageRoot: storage}

	registerClone(repo, opts)
	regPath := cloneRegistryFile(repo, opts)
	before, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatalf("first registerClone wrote nothing: %v", err)
	}

	past := time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(regPath, past, past); err != nil {
		t.Fatal(err)
	}

	registerClone(repo, opts)

	info, err := os.Stat(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("registry rewritten for an already-current entry (mtime %v, want %v)",
			info.ModTime(), past)
	}
	after, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("registry content changed on a current re-registration:\nbefore: %s\nafter:  %s",
			before, after)
	}
}

// TestLoadClonesMissingAndCorrupt pins the tolerant-load contract:
// a missing file is an empty registry; a corrupt file is an empty
// registry WITH a stderr warning (never silent, never fatal — the
// invoking clone's own workspaces are always enumerated directly, so
// starting empty only narrows the cross-clone view). registerClone
// over the corrupt file then recovers by writing a valid registry.
func TestLoadClonesMissingAndCorrupt(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := canonPath(t, t.TempDir())
	opts := Options{StorageRoot: storage}
	regPath := cloneRegistryFile(repo, opts)

	// Missing → empty, no error.
	reg, err := loadClones(repo, opts)
	if err != nil {
		t.Fatalf("loadClones on missing file: %v", err)
	}
	if len(reg) != 0 {
		t.Fatalf("loadClones on missing file = %v, want empty", reg)
	}

	// Corrupt → empty, no error, warning on stderr naming the file.
	if err := os.MkdirAll(filepath.Dir(regPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, []byte("not json {"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		reg, err = loadClones(repo, opts)
	})
	if err != nil {
		t.Fatalf("loadClones on corrupt file: %v", err)
	}
	if len(reg) != 0 {
		t.Fatalf("loadClones on corrupt file = %v, want empty", reg)
	}
	if !strings.Contains(stderr, "clone registry") ||
		!strings.Contains(stderr, "corrupt") ||
		!strings.Contains(stderr, regPath) {
		t.Errorf("stderr = %q, want a corruption warning naming %q", stderr, regPath)
	}

	// Recovery: registration over the corrupt file starts from empty
	// and writes a valid single-entry registry.
	registerClone(repo, opts)
	reg, err = loadClones(repo, opts)
	if err != nil {
		t.Fatalf("loadClones after recovery: %v", err)
	}
	if len(reg) != 1 {
		t.Fatalf("registry after recovery = %v, want exactly the invoking clone", reg)
	}
}

// TestOtherCloneRootsPrunesDeadAndMismatchedEntries pins the
// self-cleaning read: an entry whose metadata dir has vanished (the
// clone was deleted) pins nothing and is PRUNED from the file, as is
// an entry whose recorded root re-detects to a DIFFERENT clone (a
// re-created directory). Neither is reported unreachable.
func TestOtherCloneRootsPrunesDeadAndMismatchedEntries(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := canonPath(t, t.TempDir())
	opts := Options{StorageRoot: storage}
	registerClone(repo, opts)
	ownKey, err := filepath.EvalSymlinks(repo.MetadataDir())
	if err != nil {
		t.Fatal(err)
	}

	// Dead clone: its metadata dir existed once, then was deleted.
	deadDir := filepath.Join(canonPath(t, t.TempDir()), "meta")
	if err := os.MkdirAll(deadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mismatched clone: the key dir still exists, but the recorded
	// root re-detects to OUR repo, whose metadata dir is not the key.
	mismatchKey := canonPath(t, t.TempDir())

	reg, err := loadClones(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	reg[deadDir] = cloneEntry{Root: filepath.Join(deadDir, "ws"), UpdatedAt: "2000-01-01T00:00:00Z"}
	reg[mismatchKey] = cloneEntry{Root: repo.Root, UpdatedAt: "2000-01-01T00:00:00Z"}
	if err := saveClones(repo, opts, reg); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(deadDir); err != nil {
		t.Fatal(err)
	}

	roots, unreachable := otherCloneRoots(repo, opts)
	if len(roots) != 0 {
		t.Errorf("roots = %v, want empty (no live other clone exists)", roots)
	}
	if len(unreachable) != 0 {
		t.Errorf("unreachable = %v, want empty (dead/mismatched entries prune silently)", unreachable)
	}

	after, err := loadClones(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after[deadDir]; ok {
		t.Errorf("dead entry %q survived the prune", deadDir)
	}
	if _, ok := after[mismatchKey]; ok {
		t.Errorf("mismatched entry %q survived the prune", mismatchKey)
	}
	if _, ok := after[ownKey]; !ok {
		t.Errorf("own entry %q was pruned; registry = %v", ownKey, after)
	}
}

// TestOtherCloneRootsUnreachableWhenRootFailsDetect pins the
// conservative branch: a key whose metadata dir still exists but
// whose recorded root cannot be re-detected as a repository is
// UNREACHABLE — reported (so gc keeps everything) and NOT pruned
// (the clone may only be temporarily broken, e.g. an unmounted
// volume).
func TestOtherCloneRootsUnreachableWhenRootFailsDetect(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	storage := canonPath(t, t.TempDir())
	opts := Options{StorageRoot: storage}
	registerClone(repo, opts)

	metaDir := canonPath(t, t.TempDir())   // exists → not dead
	bogusRoot := canonPath(t, t.TempDir()) // empty dir → Detect fails

	reg, err := loadClones(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	reg[metaDir] = cloneEntry{Root: bogusRoot, UpdatedAt: "2000-01-01T00:00:00Z"}
	if err := saveClones(repo, opts, reg); err != nil {
		t.Fatal(err)
	}

	roots, unreachable := otherCloneRoots(repo, opts)
	if len(roots) != 0 {
		t.Errorf("roots = %v, want empty", roots)
	}
	if len(unreachable) != 1 || unreachable[0] != bogusRoot {
		t.Errorf("unreachable = %v, want [%s]", unreachable, bogusRoot)
	}

	after, err := loadClones(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after[metaDir]; !ok {
		t.Errorf("unreachable entry %q was pruned; it must survive for the next read", metaDir)
	}
}

// TestBuildGCPlanKeepsVariantPinnedByOtherClone pins the H3 data-loss
// scenario: two independent clones share `<storage>/<repo-id>/` via
// the same remote URL. A variant only clone B references MUST survive
// a gc invoked from clone A — B's registration (written by its Link)
// folds B's workspaces into A's pin walk. Also pins that BuildGCPlan
// self-registers the invoking clone.
func TestBuildGCPlanKeepsVariantPinnedByOtherClone(t *testing.T) {
	cloneA, cloneB, opts := twoCloneFixture(t)

	plan, err := BuildGCPlan(cloneA, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan(cloneA): %v", err)
	}
	if len(plan.UnreachableWorkspaces) != 0 {
		t.Errorf("UnreachableWorkspaces = %v, want empty (clone B is live)",
			plan.UnreachableWorkspaces)
	}
	variantPath := filepath.Join(opts.StorageRoot, cloneA.RepositoryID, ".env")
	if len(plan.DeleteVariants) != 0 {
		t.Fatalf("DeleteVariants = %+v, want empty — clone B pins %s",
			plan.DeleteVariants, variantPath)
	}
	kept := false
	for _, v := range plan.KeepVariants {
		if v.StoragePath == variantPath {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("KeepVariants = %+v, want it to contain %s", plan.KeepVariants, variantPath)
	}

	// BuildGCPlan self-registers: the registry now names BOTH clones.
	reg, err := loadClones(cloneA, opts)
	if err != nil {
		t.Fatal(err)
	}
	ownKey, err := filepath.EvalSymlinks(cloneA.MetadataDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg[ownKey]; !ok {
		t.Errorf("BuildGCPlan did not self-register clone A; registry = %v", reg)
	}
	if len(reg) != 2 {
		t.Errorf("registry has %d entries, want 2 (A and B)", len(reg))
	}

	// End to end: executing the plan leaves B's content readable
	// through B's workspace symlink.
	if err := ExecuteGC(cloneA, plan, opts); err != nil {
		t.Fatalf("ExecuteGC: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(cloneB.Root, ".env"))
	if err != nil {
		t.Fatalf("clone B's variant destroyed by gc from clone A: %v", err)
	}
	if string(got) != "from-clone-b\n" {
		t.Errorf("clone B's .env = %q, want %q", got, "from-clone-b\n")
	}
}

// TestBuildGCPlanUnreachableCloneForcesConservativeKeep pins the
// fail-safe: when a registered clone cannot be enumerated, gc cannot
// know what that clone pins, so EVERY variant is kept — including one
// no reachable workspace references — the clone's root is surfaced in
// UnreachableWorkspaces, and the orphaned-storage sweep is skipped
// with a note naming the clone problem.
func TestBuildGCPlanUnreachableCloneForcesConservativeKeep(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: node\n" +
			"    path: node_modules\n" +
			"    fingerprint:\n" +
			"      - \"{root}/package.json\"\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"{shared}\"'\n",
		"package.json": `{"v":"1"}`,
	})
	storage := storageIn(t, repo.Root)
	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}

	// Two fingerprint variants; only the second stays pinned.
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #1: %v", err)
	}
	writeFile(t, filepath.Join(repo.Root, "package.json"), `{"v":"2"}`)
	if err := os.Remove(filepath.Join(repo.Root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link #2: %v", err)
	}

	// Fixture sanity: with every registered clone reachable, the
	// stale variant is deletable.
	pre, err := BuildGCPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan (pre): %v", err)
	}
	if len(pre.DeleteVariants) != 1 {
		t.Fatalf("fixture: DeleteVariants = %+v, want exactly the stale variant", pre.DeleteVariants)
	}
	staleVariant := pre.DeleteVariants[0].StoragePath

	// Register an unreachable clone: metadata dir exists, root does
	// not re-detect.
	metaDir := canonPath(t, t.TempDir())
	bogusRoot := canonPath(t, t.TempDir())
	reg, err := loadClones(repo, opts)
	if err != nil {
		t.Fatal(err)
	}
	reg[metaDir] = cloneEntry{Root: bogusRoot, UpdatedAt: "2000-01-01T00:00:00Z"}
	if err := saveClones(repo, opts, reg); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildGCPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildGCPlan (unreachable clone): %v", err)
	}
	if len(plan.DeleteVariants) != 0 {
		t.Errorf("DeleteVariants = %+v, want empty — an unenumerable clone may pin anything",
			plan.DeleteVariants)
	}
	staleKept := false
	for _, v := range plan.KeepVariants {
		if v.StoragePath == staleVariant {
			staleKept = true
		}
	}
	if !staleKept {
		t.Errorf("stale variant %s missing from KeepVariants = %+v", staleVariant, plan.KeepVariants)
	}
	foundRoot := false
	for _, ws := range plan.UnreachableWorkspaces {
		if ws == bogusRoot {
			foundRoot = true
		}
	}
	if !foundRoot {
		t.Errorf("UnreachableWorkspaces = %v, want it to contain %s",
			plan.UnreachableWorkspaces, bogusRoot)
	}
	if !strings.Contains(strings.Join(plan.OrphanedStorageNotes, "\n"),
		"a clone sharing this storage could not be enumerated") {
		t.Errorf("OrphanedStorageNotes = %v, want the skipped-sweep clone note",
			plan.OrphanedStorageNotes)
	}
}

// TestBuildForgetPlanRefusesWhenOtherCloneShares pins the forget-side
// guard: while ANY other clone is registered against this storage,
// BuildForgetPlan composes a refusal naming the clone roots —
// forgetting the subtree would strand every workspace of every other
// clone. Also pins BuildForgetPlan's self-registration.
func TestBuildForgetPlanRefusesWhenOtherCloneShares(t *testing.T) {
	cloneA, cloneB, opts := twoCloneFixture(t)

	plan, err := BuildForgetPlan(cloneA, opts)
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if !strings.Contains(plan.Refusal, "other clone(s) of this repository share this storage") {
		t.Errorf("Refusal = %q, want the other-clone refusal", plan.Refusal)
	}
	if !strings.Contains(plan.Refusal, cloneB.Root) {
		t.Errorf("Refusal = %q, want it to name clone B's root %q", plan.Refusal, cloneB.Root)
	}

	reg, err := loadClones(cloneA, opts)
	if err != nil {
		t.Fatal(err)
	}
	ownKey, err := filepath.EvalSymlinks(cloneA.MetadataDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg[ownKey]; !ok {
		t.Errorf("BuildForgetPlan did not self-register clone A; registry = %v", reg)
	}
}

// TestExecuteForgetRemovesCloneRegistry pins the lifecycle tail: the
// registry describes clones of a storage subtree, so once forget has
// removed that subtree the registry and its lock file go too. Also
// pins that a clone alone in its registry (only SELF registered)
// never triggers the other-clone refusal.
func TestExecuteForgetRemovesCloneRegistry(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := canonPath(t, t.TempDir())
	opts := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	writeFile(t, filepath.Join(repo.Root, ".env"), "seed\n")
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	regPath := cloneRegistryFile(repo, opts)
	lockPath := regPath + ".wrk-lock"
	for _, p := range []string{regPath, lockPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("fixture: %s missing after Link: %v", p, err)
		}
	}

	plan, err := BuildForgetPlan(repo, opts)
	if err != nil {
		t.Fatalf("BuildForgetPlan: %v", err)
	}
	if strings.Contains(plan.Refusal, "other clone") {
		t.Fatalf("Refusal = %q — the invoking clone counted itself as an OTHER clone", plan.Refusal)
	}

	if err := ExecuteForget(repo, plan, opts); err != nil {
		t.Fatalf("ExecuteForget: %v", err)
	}

	if _, err := os.Stat(filepath.Join(storage, repo.RepositoryID)); !os.IsNotExist(err) {
		t.Errorf("storage subtree survived forget; err = %v", err)
	}
	if _, err := os.Stat(regPath); !os.IsNotExist(err) {
		t.Errorf("clone registry %s survived forget; err = %v", regPath, err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("clone registry lock %s survived forget; err = %v", lockPath, err)
	}
}
