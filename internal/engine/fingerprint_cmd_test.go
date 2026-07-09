package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/fingerprint"
	"github.com/blaineventurine/wrk/internal/repository"
)

// linkedFingerprintedRepo builds a git repo whose config declares a
// single fingerprinted resource `node_modules` keyed by manifest.json,
// links it into shared storage, and returns the repo, the resolved
// storage root, the workspace path of the resource, and Options
// pre-populated with the storage root. Tests that need "linked,
// fingerprinted resource" state start here.
func linkedFingerprintedRepo(t *testing.T, manifestContents string) (repo *repository.Repository, storage string, wsPath string, opts Options) {
	t.Helper()
	repo = newTestRepo(t)
	storage = storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/manifest.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), manifestContents)
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg", "marker"), "v1\n")

	opts = Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, opts); err != nil {
		t.Fatalf("Link: %v", err)
	}

	wsPath = filepath.Join(repo.Root, "node_modules")
	// Sanity-check: after Link the workspace path must be a symlink.
	// If it isn't, every downstream assertion is bogus.
	if info, err := os.Lstat(wsPath); err != nil {
		t.Fatalf("Lstat wsPath: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("workspace path is not a symlink after Link: %v", info.Mode())
	}
	return repo, storage, wsPath, opts
}

// TestFingerprintOneUnchangedResource pins the happy-path invariant:
// on a workspace that was just linked, FingerprintOne sees the
// currently-pinned digest match the freshly-computed one — no
// spurious drift. It also pins the shape of Current.Inputs so the CLI
// layer can rely on it: one entry per configured input, repository-
// relative path, Exists=true, SizeBytes matching os.Stat.
func TestFingerprintOneUnchangedResource(t *testing.T) {
	repo, _, _, opts := linkedFingerprintedRepo(t, `{"v":1}`)

	report, err := FingerprintOne(repo, "node", opts)
	if err != nil {
		t.Fatalf("FingerprintOne: %v", err)
	}

	if report.Changed {
		t.Errorf("Changed = true, want false on freshly-linked workspace")
	}
	if report.Current.Fingerprint == "" {
		t.Fatalf("Current.Fingerprint is empty")
	}
	if got, want := len(report.Current.Fingerprint), fingerprint.Length; got != want {
		t.Errorf("Current.Fingerprint length = %d, want %d", got, want)
	}
	if report.Current.Fingerprint != report.Pinned.Fingerprint {
		t.Errorf("Current.Fingerprint = %q, Pinned.Fingerprint = %q; want equal",
			report.Current.Fingerprint, report.Pinned.Fingerprint)
	}
	if report.Current.StoragePath != report.Pinned.StoragePath {
		t.Errorf("Current.StoragePath = %q, Pinned.StoragePath = %q; want equal",
			report.Current.StoragePath, report.Pinned.StoragePath)
	}

	if len(report.Current.Inputs) != 1 {
		t.Fatalf("Current.Inputs = %+v, want 1 entry", report.Current.Inputs)
	}
	in := report.Current.Inputs[0]
	if in.Path != "manifest.json" {
		t.Errorf("Inputs[0].Path = %q, want %q", in.Path, "manifest.json")
	}
	if !in.Exists {
		t.Errorf("Inputs[0].Exists = false, want true (file was just written)")
	}
	info, err := os.Stat(filepath.Join(repo.Root, "manifest.json"))
	if err != nil {
		t.Fatalf("stat manifest.json: %v", err)
	}
	if in.SizeBytes != info.Size() {
		t.Errorf("Inputs[0].SizeBytes = %d, want %d", in.SizeBytes, info.Size())
	}

	// Pinned.Inputs is documented as "always nil for now". Pin that
	// contract so a future recorder-based enrichment can't quietly
	// break CLI consumers.
	if report.Pinned.Inputs != nil {
		t.Errorf("Pinned.Inputs = %+v, want nil", report.Pinned.Inputs)
	}
}

// TestFingerprintOneDetectsStale is the load-bearing contract: after
// a fingerprint input is mutated on disk, FingerprintOne MUST report
// Changed=true even though the workspace symlink still points at the
// old variant. If this ever regresses, `wrk fingerprint` becomes
// useless as a drift detector.
func TestFingerprintOneDetectsStale(t *testing.T) {
	repo, _, _, opts := linkedFingerprintedRepo(t, `{"v":1}`)

	// Rewrite the manifest so the freshly-computed fingerprint
	// differs from what the symlink still pins.
	writeFile(t, filepath.Join(repo.Root, "manifest.json"), `{"v":2}`)

	report, err := FingerprintOne(repo, "node", opts)
	if err != nil {
		t.Fatalf("FingerprintOne: %v", err)
	}

	if !report.Changed {
		t.Errorf("Changed = false, want true after mutating an input")
	}
	if report.Current.Fingerprint == report.Pinned.Fingerprint {
		t.Errorf("Current == Pinned after input mutation: both %q", report.Current.Fingerprint)
	}
	if report.Pinned.Fingerprint == "" {
		t.Errorf("Pinned.Fingerprint empty; workspace should still be a symlink")
	}
	if report.Current.StoragePath == report.Pinned.StoragePath {
		t.Errorf("StoragePath unchanged after fingerprint change: %q", report.Current.StoragePath)
	}
}

// TestFingerprintOneEmptyPinnedForDetachedWorkspace pins that a
// detached workspace — no symlink at the resource path — surfaces as
// Pinned = zero-value, Changed = true. That is the truthful signal
// downstream tooling needs to say "re-link before running".
func TestFingerprintOneEmptyPinnedForDetachedWorkspace(t *testing.T) {
	repo, _, wsPath, opts := linkedFingerprintedRepo(t, `{"v":1}`)

	if err := Detach(repo, opts); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	// After Detach the workspace path should be a real directory again.
	info, err := os.Lstat(wsPath)
	if err != nil {
		t.Fatalf("Lstat post-detach: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("workspace path is still a symlink after Detach: %v", info.Mode())
	}

	report, err := FingerprintOne(repo, "node", opts)
	if err != nil {
		t.Fatalf("FingerprintOne: %v", err)
	}

	if report.Pinned.Fingerprint != "" {
		t.Errorf("Pinned.Fingerprint = %q, want \"\" for detached workspace", report.Pinned.Fingerprint)
	}
	if report.Pinned.StoragePath != "" {
		t.Errorf("Pinned.StoragePath = %q, want \"\" for detached workspace", report.Pinned.StoragePath)
	}
	if report.Current.Fingerprint == "" {
		t.Errorf("Current.Fingerprint is empty; inputs on disk should still compute a digest")
	}
	if !report.Changed {
		t.Errorf("Changed = false, want true when Pinned is empty and Current is populated")
	}
}

// TestFingerprintOneUnknownResourceErrors pins that an unknown resource
// name is a clean, actionable error at the engine layer — no partial
// report, no nil deref. The message MUST identify the offending name
// so the CLI can pass it straight through.
func TestFingerprintOneUnknownResourceErrors(t *testing.T) {
	repo := newTestRepo(t)
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n")

	report, err := FingerprintOne(repo, "nonexistent", Options{StorageRoot: storageIn(t, repo.Root)})
	if err == nil {
		t.Fatalf("FingerprintOne returned nil error; report=%+v", report)
	}
	if report != nil {
		t.Errorf("report = %+v, want nil on error", report)
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error %q lacks \"not configured\"", err.Error())
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q lacks the requested resource name", err.Error())
	}
}

// TestFingerprintOneUnfingerprintedResourceErrors pins that
// FingerprintOne refuses to answer for a resource with no
// fingerprint block — there is nothing to compare, and silently
// returning "" would just confuse the caller.
func TestFingerprintOneUnfingerprintedResourceErrors(t *testing.T) {
	repo := newTestRepo(t)
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n  - name: env\n    path: .env\n")

	report, err := FingerprintOne(repo, "env", Options{StorageRoot: storageIn(t, repo.Root)})
	if err == nil {
		t.Fatalf("FingerprintOne returned nil error; report=%+v", report)
	}
	if report != nil {
		t.Errorf("report = %+v, want nil on error", report)
	}
	if !strings.Contains(err.Error(), "not fingerprinted") {
		t.Errorf("error %q lacks \"not fingerprinted\"", err.Error())
	}
}

// TestFingerprintOneNilRepoErrors pins that a nil repo is rejected
// before it can trip a downstream nil deref. Callers of the engine
// layer aren't guaranteed to have wired a real repo — the CLI is,
// but subagents and tests are not — so this guard is load-bearing.
func TestFingerprintOneNilRepoErrors(t *testing.T) {
	report, err := FingerprintOne(nil, "anything", Options{})
	if err == nil {
		t.Fatalf("FingerprintOne(nil) returned nil error; report=%+v", report)
	}
	if report != nil {
		t.Errorf("report = %+v, want nil on error", report)
	}
}

// TestFingerprintOneInputPathsRelativeToRoot pins that
// Current.Inputs[i].Path is repo-relative with forward slashes, even
// when the raw config input lives in a nested directory. Absolute
// paths would leak the caller's tempdir into the output and break
// any CLI consumer that stitches paths together.
func TestFingerprintOneInputPathsRelativeToRoot(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// Two inputs in nested subdirs so the test would fail if the
	// implementation kept absolute paths OR flattened basenames.
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/subdir/a.txt\"\n"+
			"      - \"{root}/nested/dir/b.txt\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "subdir", "a.txt"), "A\n")
	writeFile(t, filepath.Join(repo.Root, "nested", "dir", "b.txt"), "B\n")
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg", "marker"), "v1\n")

	report, err := FingerprintOne(repo, "node", Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("FingerprintOne: %v", err)
	}

	// Inputs come back sorted by nothing — implementation preserves
	// resolver order — so index paths into a set for robust checking.
	got := map[string]FingerprintInput{}
	for _, in := range report.Current.Inputs {
		got[in.Path] = in
	}

	// Every expected key must be exactly the forward-slash repo-
	// relative form. Anything absolute or with backslashes is a bug.
	for _, want := range []string{"subdir/a.txt", "nested/dir/b.txt"} {
		in, ok := got[want]
		if !ok {
			t.Errorf("Inputs missing %q; got keys=%v", want, keysOf(got))
			continue
		}
		if !in.Exists {
			t.Errorf("Inputs[%q].Exists = false, want true", want)
		}
		if filepath.IsAbs(in.Path) {
			t.Errorf("Inputs[%q].Path is absolute: %q", want, in.Path)
		}
		if strings.Contains(in.Path, "\\") {
			t.Errorf("Inputs[%q].Path contains backslashes: %q", want, in.Path)
		}
	}
}

// TestFingerprintOneReportsMissingInputs pins that a declared input
// which is not on disk surfaces as Exists=false without breaking
// fingerprint computation. This is the shape the CLI needs to say
// "you're linking against a fingerprint that assumes X is missing".
func TestFingerprintOneReportsMissingInputs(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    fingerprint:\n"+
			"      - \"{root}/present.json\"\n"+
			"      - \"{root}/missing.json\"\n",
	)
	writeFile(t, filepath.Join(repo.Root, "present.json"), "hi\n")
	writeFile(t, filepath.Join(repo.Root, "node_modules", "pkg", "marker"), "v1\n")
	// Deliberately do NOT create missing.json.

	report, err := FingerprintOne(repo, "node", Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("FingerprintOne: %v", err)
	}
	if report.Current.Fingerprint == "" {
		t.Fatalf("Current.Fingerprint empty; fingerprint.go should still return a digest with missing inputs")
	}

	byPath := map[string]FingerprintInput{}
	for _, in := range report.Current.Inputs {
		byPath[in.Path] = in
	}

	present, ok := byPath["present.json"]
	if !ok {
		t.Fatalf("Inputs missing entry for present.json; got=%v", keysOf(byPath))
	}
	if !present.Exists {
		t.Errorf("present.json Exists = false, want true")
	}
	if present.SizeBytes <= 0 {
		t.Errorf("present.json SizeBytes = %d, want > 0", present.SizeBytes)
	}

	missing, ok := byPath["missing.json"]
	if !ok {
		t.Fatalf("Inputs missing entry for missing.json; got=%v", keysOf(byPath))
	}
	if missing.Exists {
		t.Errorf("missing.json Exists = true, want false")
	}
	if missing.SizeBytes != 0 {
		t.Errorf("missing.json SizeBytes = %d, want 0", missing.SizeBytes)
	}
}

func keysOf(m map[string]FingerprintInput) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
