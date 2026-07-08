package repository

import (
	"os/exec"
	"strings"
	"testing"
)

// TestJJCommonDirRequiresColocation pins S10: any failure from
// `jj git root` is wrapped with wrk's colocation requirement so users
// understand why detection failed instead of chasing jj's internal
// "not a colocated repo" error.
func TestJJCommonDirRequiresColocation(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}

	// Empty temp dir — no `.jj`, no `.git`. `jj git root` will fail;
	// the wrap must fire regardless of the underlying jj message so
	// users see the requirement even when jj's phrasing changes
	// between releases.
	_, err := jjBackend{}.commonDir(t.TempDir())
	if err == nil {
		t.Fatal("jjBackend.commonDir: expected error in non-repo directory")
	}
	if !strings.Contains(err.Error(), "colocated") {
		t.Fatalf("error missing colocation guidance: %v", err)
	}
}
