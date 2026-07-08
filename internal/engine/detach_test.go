package engine

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/repository"
)

// newTestRepo returns a Repository rooted at a fresh temp dir with an
// initialized empty git backend. Used by the registry tests below because
// the repository package's constructor is unexported; a real `git init`
// is the only way to satisfy Detect from outside the package, and it is
// still cheap (<50ms on macOS/Linux).
func newTestRepo(t *testing.T) *repository.Repository {
	t.Helper()

	dir := t.TempDir()

	cmd := exec.Command("git", "init", "-q", dir)
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	repo, err := repository.Detect(dir, repository.Auto)
	if err != nil {
		t.Fatalf("repository.Detect: %v", err)
	}
	return repo
}

// readRegistry reads the on-disk registry directly, so a test that
// exercises recordDetached also confirms the JSON round-trip.
func readRegistry(t *testing.T, repo *repository.Repository) detachRegistry {
	t.Helper()

	data, err := os.ReadFile(registryPath(repo))
	if os.IsNotExist(err) {
		return detachRegistry{}
	}
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	reg := detachRegistry{}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("unmarshal registry: %v", err)
	}
	return reg
}

// sortedRegistryEntry returns the sorted list of relative paths recorded
// for root — order in the registry is preserved by union() (existing
// first, new second) but tests want set equality.
func sortedRegistryEntry(reg detachRegistry, root string) []string {
	entry := append([]string(nil), reg[root]...)
	sort.Strings(entry)
	return entry
}

func TestRecordDetachedIdempotent_EmptyDoesNotWipe(t *testing.T) {
	repo := newTestRepo(t)

	if err := recordDetached(repo, []string{"a", "b"}); err != nil {
		t.Fatalf("first recordDetached: %v", err)
	}
	// Second call with no new paths (the "second `wrk detach` when nothing
	// is left to detach" scenario) must NOT wipe the entry.
	if err := recordDetached(repo, nil); err != nil {
		t.Fatalf("second recordDetached: %v", err)
	}

	reg := readRegistry(t, repo)
	got := sortedRegistryEntry(reg, repo.Root)
	want := []string{"a", "b"}
	if !equalSlice(got, want) {
		t.Fatalf("registry entry = %v, want %v", got, want)
	}
}

func TestRecordDetachedIncremental(t *testing.T) {
	repo := newTestRepo(t)

	if err := recordDetached(repo, []string{"a"}); err != nil {
		t.Fatalf("first recordDetached: %v", err)
	}
	if err := recordDetached(repo, []string{"b"}); err != nil {
		t.Fatalf("second recordDetached: %v", err)
	}

	reg := readRegistry(t, repo)
	got := sortedRegistryEntry(reg, repo.Root)
	want := []string{"a", "b"}
	if !equalSlice(got, want) {
		t.Fatalf("registry entry = %v, want %v", got, want)
	}
}

func TestRecordDetachedDeduplicates(t *testing.T) {
	repo := newTestRepo(t)

	if err := recordDetached(repo, []string{"a", "b"}); err != nil {
		t.Fatalf("first recordDetached: %v", err)
	}
	if err := recordDetached(repo, []string{"b", "c"}); err != nil {
		t.Fatalf("second recordDetached: %v", err)
	}

	reg := readRegistry(t, repo)
	got := sortedRegistryEntry(reg, repo.Root)
	want := []string{"a", "b", "c"}
	if !equalSlice(got, want) {
		t.Fatalf("registry entry = %v, want %v", got, want)
	}

	// And no duplicate `b` sneaks in — length check independent of sort.
	if n := len(reg[repo.Root]); n != 3 {
		t.Fatalf("registry entry length = %d, want 3", n)
	}
}

func TestRecordDetachedPreservesOrder(t *testing.T) {
	repo := newTestRepo(t)

	if err := recordDetached(repo, []string{"first", "second"}); err != nil {
		t.Fatalf("first recordDetached: %v", err)
	}
	if err := recordDetached(repo, []string{"third"}); err != nil {
		t.Fatalf("second recordDetached: %v", err)
	}

	// union() documents: existing first, then new. Guard that contract so
	// downstream code can rely on stable ordering.
	reg := readRegistry(t, repo)
	got := reg[repo.Root]
	want := []string{"first", "second", "third"}
	if !equalSlice(got, want) {
		t.Fatalf("registry entry = %v, want %v", got, want)
	}
}

func TestClearDetachedRemovesEntry(t *testing.T) {
	repo := newTestRepo(t)

	if err := recordDetached(repo, []string{"a"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}
	if err := clearDetached(repo); err != nil {
		t.Fatalf("clearDetached: %v", err)
	}

	reg := readRegistry(t, repo)
	if _, ok := reg[repo.Root]; ok {
		t.Fatalf("clearDetached left entry for %q: %v", repo.Root, reg)
	}
}

func TestClearDetachedMissingEntryIsNoop(t *testing.T) {
	repo := newTestRepo(t)

	// clearDetached with no prior record must not error and must not
	// create a registry file with a bogus entry.
	if err := clearDetached(repo); err != nil {
		t.Fatalf("clearDetached: %v", err)
	}
}

func TestLoadRegistryToleratesCorruption(t *testing.T) {
	repo := newTestRepo(t)

	// Seed a well-formed entry, then trash the file to garbage.
	if err := recordDetached(repo, []string{"a"}); err != nil {
		t.Fatalf("recordDetached: %v", err)
	}
	if err := os.WriteFile(registryPath(repo), []byte("not json {"), 0o644); err != nil {
		t.Fatalf("corrupt registry: %v", err)
	}

	reg, err := loadRegistry(repo)
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	if len(reg) != 0 {
		t.Fatalf("loadRegistry from corrupt file = %v, want empty", reg)
	}

	// And a subsequent record recovers cleanly — corrupt input is
	// treated as empty, so the new call establishes a fresh entry.
	if err := recordDetached(repo, []string{"b"}); err != nil {
		t.Fatalf("recordDetached after corruption: %v", err)
	}
	got := sortedRegistryEntry(readRegistry(t, repo), repo.Root)
	if want := []string{"b"}; !equalSlice(got, want) {
		t.Fatalf("registry entry after recovery = %v, want %v", got, want)
	}
}

// TestLoadRegistryLogsCorruption pins M12: the recovery-from-empty
// path is still correct (existing test above), but a corrupt registry
// must also announce itself on stderr so an operator can see what got
// wiped on the next save. Silence used to hide real filesystem issues.
func TestLoadRegistryLogsCorruption(t *testing.T) {
	repo := newTestRepo(t)

	// Seed the registry path with garbage.
	path := registryPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json {"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture stderr for the duration of the call.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	reg, loadErr := loadRegistry(repo)

	// Restore stderr and collect the pipe output.
	_ = w.Close()
	os.Stderr = origStderr
	buf, _ := io.ReadAll(r)

	if loadErr != nil {
		t.Fatalf("loadRegistry: %v", loadErr)
	}
	if len(reg) != 0 {
		t.Fatalf("loadRegistry from corrupt file = %v, want empty", reg)
	}
	out := string(buf)
	if !strings.Contains(out, "detach registry") ||
		!strings.Contains(out, "corrupt") ||
		!strings.Contains(out, "treating as empty") {
		t.Fatalf("stderr = %q, want corruption warning mentioning the file and 'treating as empty'", out)
	}
	if !strings.Contains(out, path) {
		t.Fatalf("stderr = %q, want mention of registry path %q", out, path)
	}
}

func TestRegistryPathLivesUnderMetadataDir(t *testing.T) {
	repo := newTestRepo(t)

	// Sanity: registry file MUST live inside the repository metadata
	// directory, otherwise a `git clean` / bare checkout would clobber it.
	got := registryPath(repo)
	want := filepath.Join(repo.MetadataDir(), "wrk", "detached.json")
	if got != want {
		t.Fatalf("registryPath = %q, want %q", got, want)
	}
}

// TestSaveRegistryIsAtomic covers D3: saveRegistry writes via a tmp
// file and rename, so a pre-existing stray `<path>.tmp` from a
// previous crash is overwritten and never resurfaces as garbage.
// Verifies (a) the final file contains the new content, (b) the tmp
// file is gone after a successful save (rename consumed it).
func TestSaveRegistryIsAtomic(t *testing.T) {
	repo := newTestRepo(t)

	path := registryPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate a crashed prior write: garbage sitting at <path>.tmp. A
	// correct atomic save must overwrite it and clear the way.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("garbage-from-a-prior-crash"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := detachRegistry{
		repo.Root: []string{"node_modules", "vendor"},
	}
	if err := saveRegistry(repo, reg); err != nil {
		t.Fatalf("saveRegistry: %v", err)
	}

	// Final path holds the new content.
	got := readRegistry(t, repo)
	if diff := len(got); diff != 1 {
		t.Fatalf("registry entry count = %d, want 1", diff)
	}
	entries := sortedRegistryEntry(got, repo.Root)
	want := []string{"node_modules", "vendor"}
	sort.Strings(want)
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want %v", entries, want)
	}
	for i := range entries {
		if entries[i] != want[i] {
			t.Fatalf("entries = %v, want %v", entries, want)
		}
	}

	// The tmp file was renamed into place, not left behind.
	if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
		t.Errorf("expected tmp file removed after rename, Lstat err=%v", err)
	}
}

// TestSaveRegistryOverwritesPriorContent verifies the intended
// round-trip semantics: saving twice replaces the file with the
// second registry rather than merging or corrupting it.
func TestSaveRegistryOverwritesPriorContent(t *testing.T) {
	repo := newTestRepo(t)

	first := detachRegistry{repo.Root: []string{"a"}}
	if err := saveRegistry(repo, first); err != nil {
		t.Fatalf("first saveRegistry: %v", err)
	}

	second := detachRegistry{repo.Root: []string{"b", "c"}}
	if err := saveRegistry(repo, second); err != nil {
		t.Fatalf("second saveRegistry: %v", err)
	}

	got := readRegistry(t, repo)
	entries := sortedRegistryEntry(got, repo.Root)
	want := []string{"b", "c"}
	if len(entries) != len(want) {
		t.Fatalf("entries = %v, want %v", entries, want)
	}
	for i := range entries {
		if entries[i] != want[i] {
			t.Fatalf("entries = %v, want %v", entries, want)
		}
	}

	// No orphan tmp files.
	tmp := registryPath(repo) + ".tmp"
	if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
		t.Errorf("expected no leftover tmp file, Lstat err=%v", err)
	}
}
