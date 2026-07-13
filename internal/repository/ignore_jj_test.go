package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrepareExcludesFilesFromJJStatus pins the end-to-end exclusion
// contract for colocated jj repositories: Prepare writes to
// <gitdir>/info/exclude, and jj honors git excludes in colocated mode,
// so an excluded file must NOT appear in `jj status` while a regular
// new file (positive control) MUST. Without the positive control a
// silently-broken jj setup that lists nothing at all would pass.
func TestPrepareExcludesFilesFromJJStatus(t *testing.T) {
	skipIfNoJJ(t)
	skipIfNoGit(t)
	isolateJJConfig(t)

	dir := t.TempDir()
	initColocatedJJRepo(t, dir)

	repo, err := Detect(dir, Auto)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if repo.VCS() != JJ {
		t.Fatalf("detected VCS = %q, want %q (colocated repos must prefer jj)",
			repo.VCS(), JJ)
	}

	if err := repo.Prepare("blob.bin"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	for _, name := range []string{"blob.bin", "visible.txt"} {
		if err := os.WriteFile(
			filepath.Join(dir, name),
			[]byte("payload\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("jj", "status")
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("jj status: %v\nstderr:\n%s", err, stderr.String())
	}
	status := string(out)

	if strings.Contains(status, "blob.bin") {
		t.Errorf("excluded file leaked into jj status:\n%s", status)
	}
	if !strings.Contains(status, "visible.txt") {
		t.Errorf("positive control missing — jj status did not report "+
			"the non-excluded file:\n%s\nstderr:\n%s", status, stderr.String())
	}
}
