package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestIgnorePreparerAddsWrkScratchPatterns pins the M10 fix: every
// Link run seeds `.git/info/exclude` with wildcard patterns that
// match the executor's staging files (`.wrk-tmp`, `.wrk-backup`,
// `.wrk-lock`). Without these, a crash mid-Symlink or mid-Detach
// leaves scratch files that `git status` reports as untracked — for
// a directory resource, potentially many MB of stray content the
// user could accidentally commit.
//
// The assertion looks for the exact wildcard pattern text as its own
// line, so a regression that appended a resource path *containing*
// the suffix (rather than the wildcard itself) would still be
// caught.
func TestIgnorePreparerAddsWrkScratchPatterns(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: env\n    path: .env\n")
	writeFile(t, filepath.Join(repo.Root, ".env"), "hello=world\n")

	if err := Link(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	exclude := filepath.Join(repo.MetadataDir(), "info", "exclude")
	data, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatalf("read exclude file: %v", err)
	}

	// Each pattern must appear as its own line (surrounded by newlines
	// so a substring embedded in some resource path can't satisfy the
	// assertion by accident).
	lines := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		lines[strings.TrimSpace(line)] = true
	}

	for _, want := range []string{"*.wrk-tmp", "*.wrk-backup", "*.wrk-lock"} {
		if !lines[want] {
			t.Errorf(
				"exclude file missing wildcard pattern %q\n---\n%s",
				want, data,
			)
		}
	}
}
