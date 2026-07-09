package engine

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorReportsCleanRepo pins the happy-path shape: a linked,
// well-configured repository produces an empty Issues slice, every
// identity field is populated, ConfigValid is true, and the storage
// tree carries some bytes (the linked resource left them there).
func TestDoctorReportsCleanRepo(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: env\n" +
			"    path: .env\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"$(dirname \"{shared}\")\" && printf hello > \"{shared}\"'\n",
	})
	storage := storageIn(t, repo.Root)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	report, err := Doctor(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Root != repo.Root {
		t.Errorf("Root = %q, want %q", report.Root, repo.Root)
	}
	if report.RepositoryID == "" || report.RepositoryID != repo.RepositoryID {
		t.Errorf("RepositoryID = %q, want %q", report.RepositoryID, repo.RepositoryID)
	}
	if report.VCS != "git" {
		t.Errorf("VCS = %q, want %q", report.VCS, "git")
	}
	if !report.Checks.ConfigValid {
		t.Errorf("ConfigValid = false, want true (ConfigError = %q)",
			report.Checks.ConfigError)
	}
	if report.Checks.ConfigError != "" {
		t.Errorf("ConfigError = %q, want empty", report.Checks.ConfigError)
	}
	if len(report.Checks.GhostWorkspaces) != 0 {
		t.Errorf("GhostWorkspaces = %v, want empty", report.Checks.GhostWorkspaces)
	}
	if len(report.Checks.OrphanedLocks) != 0 ||
		len(report.Checks.StaleProvisioning) != 0 ||
		len(report.Checks.StaleDeleting) != 0 ||
		len(report.Checks.StaleForgetting) != 0 {
		t.Errorf("expected no bookkeeping cruft, got %+v", report.Checks)
	}
	if report.Checks.StorageSizeBytes <= 0 {
		t.Errorf("StorageSizeBytes = %d, want > 0", report.Checks.StorageSizeBytes)
	}
	if len(report.Issues) != 0 {
		t.Errorf("Issues = %v, want empty", report.Issues)
	}
}

// TestDoctorReportsInvalidConfig pins the config-parse-failure path:
// an invalid `.wrk.yml` surfaces as ConfigValid=false, populates
// ConfigError with the underlying load error, and prepends the
// standard "config invalid: …" hint to Issues.
func TestDoctorReportsInvalidConfig(t *testing.T) {
	repo := newTestRepoWithHead(t, nil)
	// A resource entry with a path but no name — this trips the
	// "name is required" branch covered by TestValidateRejects.
	writeConfig(t, repo.Root, ".wrk.yml",
		"resources:\n  - path: some_file\n")

	report, err := Doctor(repo, Options{StorageRoot: storageIn(t, repo.Root)})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Checks.ConfigValid {
		t.Fatalf("ConfigValid = true, want false")
	}
	if !strings.Contains(report.Checks.ConfigError, "name is required") {
		t.Errorf("ConfigError = %q, want to contain %q",
			report.Checks.ConfigError, "name is required")
	}
	if len(report.Issues) == 0 || !strings.HasPrefix(report.Issues[0], "config invalid:") {
		t.Fatalf("Issues[0] = %q, want prefix %q", report.Issues, "config invalid:")
	}
	if !strings.Contains(report.Issues[0], "name is required") {
		t.Errorf("Issues[0] = %q, want to include %q",
			report.Issues[0], "name is required")
	}
}

// TestDoctorReportsGhostWorkspace: rm -rf a secondary worktree
// out-of-band. Doctor's GhostWorkspaces MUST include the missing
// worktree's path, and Issues MUST carry a `wrk gc` hint mentioning
// ghosts.
func TestDoctorReportsGhostWorkspace(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})

	// Create a real ghost worktree so DetectGhosts sees it.
	tempParent := filepath.Dir(repo.Root)
	feature := filepath.Join(tempParent, "feature")
	cmd := exec.Command("git", "-C", repo.Root, "worktree", "add", "-b", "feature", feature)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := os.RemoveAll(feature); err != nil {
		t.Fatal(err)
	}
	// Canonicalize for the comparison — DetectGhosts hands back
	// canonicalized paths, and on macOS the temp dir lives under a
	// /var → /private/var symlink.
	featureCanon := canonPath(t, filepath.Dir(feature))
	wantGhost := filepath.Join(featureCanon, filepath.Base(feature))

	report, err := Doctor(repo, Options{StorageRoot: storageIn(t, repo.Root)})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	found := false
	for _, g := range report.Checks.GhostWorkspaces {
		if g == wantGhost {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GhostWorkspaces = %v, want to contain %q",
			report.Checks.GhostWorkspaces, wantGhost)
	}
	ghostIssue := ""
	for _, iss := range report.Issues {
		if strings.Contains(iss, "ghost") {
			ghostIssue = iss
			break
		}
	}
	if ghostIssue == "" {
		t.Errorf("Issues = %v, want an entry mentioning %q", report.Issues, "ghost")
	}
	if !strings.Contains(ghostIssue, "wrk gc") {
		t.Errorf("ghost Issue = %q, want to mention %q", ghostIssue, "wrk gc")
	}
}

// TestDoctorReportsOrphanedLock seeds an orphaned <variant>.wrk-lock
// file under storage (no matching variant subdirectory). Doctor MUST
// surface it via Checks.OrphanedLocks and add the "N stale bookkeeping"
// hint to Issues.
func TestDoctorReportsOrphanedLock(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n  - name: env\n    path: .env\n",
	})
	storage := storageIn(t, repo.Root)

	// Seed a lock file with no corresponding variant — mirrors the
	// TestCleanBookkeepingDetectFindsOrphanedLock layout.
	resourceDir := filepath.Join(storage, repo.RepositoryID, "node_modules")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(resourceDir, "5fd1d0d6.wrk-lock")
	writeFile(t, orphan, "")

	report, err := Doctor(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(report.Checks.OrphanedLocks) != 1 || report.Checks.OrphanedLocks[0] != orphan {
		t.Fatalf("OrphanedLocks = %v, want [%q]",
			report.Checks.OrphanedLocks, orphan)
	}
	// The bookkeeping hint MUST be present and start with a count.
	bookkeeper := ""
	for _, iss := range report.Issues {
		if strings.Contains(iss, "stale bookkeeping") {
			bookkeeper = iss
			break
		}
	}
	if bookkeeper == "" {
		t.Fatalf("Issues = %v, want an entry mentioning 'stale bookkeeping'",
			report.Issues)
	}
	if !strings.HasPrefix(bookkeeper, "1 stale bookkeeping") {
		t.Errorf("bookkeeping Issue = %q, want prefix %q",
			bookkeeper, "1 stale bookkeeping")
	}
	if !strings.Contains(bookkeeper, "wrk gc") {
		t.Errorf("bookkeeping Issue = %q, want to mention %q",
			bookkeeper, "wrk gc")
	}
}

// TestDoctorReportsStorageSize pins the storage-size probe against
// treeSize as ground truth: Doctor MUST report the same number the
// canonical helper would.
func TestDoctorReportsStorageSize(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources:\n" +
			"  - name: env\n" +
			"    path: .env\n" +
			"    hooks:\n" +
			"      initialize:\n" +
			"        - run: sh -c 'mkdir -p \"$(dirname \"{shared}\")\" && dd if=/dev/zero of=\"{shared}\" bs=1024 count=1'\n",
	})
	storage := storageIn(t, repo.Root)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	wantSize, err := treeSize(filepath.Join(storage, repo.RepositoryID))
	if err != nil {
		t.Fatalf("treeSize: %v", err)
	}
	if wantSize <= 0 {
		t.Fatalf("treeSize ground truth = %d, want > 0", wantSize)
	}

	report, err := Doctor(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Checks.StorageSizeBytes != wantSize {
		t.Errorf("StorageSizeBytes = %d, want %d",
			report.Checks.StorageSizeBytes, wantSize)
	}
}

// TestDoctorEmptyStorage: a repo that has never been linked has no
// storage subtree at all. Doctor MUST report zero bytes, no cruft,
// valid config, and an empty Issues slice.
func TestDoctorEmptyStorage(t *testing.T) {
	repo := newTestRepoWithHead(t, map[string]string{
		".wrk.yml": "resources: []\n",
	})
	storage := storageIn(t, repo.Root)

	report, err := Doctor(repo, Options{StorageRoot: storage})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Checks.StorageSizeBytes != 0 {
		t.Errorf("StorageSizeBytes = %d, want 0", report.Checks.StorageSizeBytes)
	}
	if !report.Checks.ConfigValid {
		t.Errorf("ConfigValid = false, want true (ConfigError = %q)",
			report.Checks.ConfigError)
	}
	if len(report.Checks.OrphanedLocks) != 0 ||
		len(report.Checks.StaleProvisioning) != 0 ||
		len(report.Checks.StaleDeleting) != 0 ||
		len(report.Checks.StaleForgetting) != 0 {
		t.Errorf("expected no bookkeeping cruft, got %+v", report.Checks)
	}
	if len(report.Issues) != 0 {
		t.Errorf("Issues = %v, want empty", report.Issues)
	}
}

// TestDoctorNilRepoErrors: a nil repo is a caller bug, not a
// filesystem condition. Doctor MUST return an error rather than a
// half-populated report.
func TestDoctorNilRepoErrors(t *testing.T) {
	_, err := Doctor(nil, Options{})
	if err == nil {
		t.Fatal("expected error for nil repo")
	}
	if !strings.Contains(err.Error(), "nil repo") {
		t.Errorf("error = %q, want to mention %q", err.Error(), "nil repo")
	}
}
