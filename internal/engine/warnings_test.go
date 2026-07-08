package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestPrintWarningsWritesEachEntry pins the plumbing: printWarnings
// surfaces every advisory the config Load produced, one per line,
// prefixed with `!` so it visually separates from plan output.
func TestPrintWarningsWritesEachEntry(t *testing.T) {
	cfg := &config.Config{
		Warnings: []string{
			"first warning",
			"second warning",
		},
	}

	var buf bytes.Buffer
	printWarnings(cfg, &buf)

	got := buf.String()
	for _, want := range []string{"first warning", "second warning"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}
	if !strings.Contains(got, "!") {
		t.Errorf("output missing warning prefix `!`\n---\n%s", got)
	}
	if lines := strings.Count(got, "\n"); lines != 2 {
		t.Errorf("expected 2 lines, got %d:\n%s", lines, got)
	}
}

func TestPrintWarningsIsNoopForNilOrEmpty(t *testing.T) {
	var buf bytes.Buffer

	printWarnings(nil, &buf)
	if buf.Len() != 0 {
		t.Errorf("nil cfg: expected no output, got %q", buf.String())
	}

	printWarnings(&config.Config{}, &buf)
	if buf.Len() != 0 {
		t.Errorf("no warnings: expected no output, got %q", buf.String())
	}
}

// TestConfigLoadPassesWarningsThrough is the integration pin: buildPlan
// (used by Link/Relink/Detach) surfaces cfg.Warnings to options.Stdout,
// so `wrk link` on a repo with a path-redirecting local override shows
// the advisory to the user.
func TestConfigLoadPassesWarningsThrough(t *testing.T) {
	repo := newTestRepo(t)

	writeConfig(t, repo.Root, config.Filename, `
resources:
  - name: env
    path: .env
`)
	writeConfig(t, repo.Root, config.LocalFilename, `
resources:
  - name: env
    path: env.dev
`)

	// Touch both paths so the resolver has something to plan against;
	// the plan itself is not what this test cares about — the warning
	// preamble is.
	if err := os.WriteFile(filepath.Join(repo.Root, "env.dev"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	_, err := BuildLinkPlan(repo, Options{
		Stdout:      &out,
		StorageRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildLinkPlan: %v", err)
	}

	got := out.String()
	for _, want := range []string{`"env"`, `".env"`, `"env.dev"`, "!"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning output missing %q\n---\n%s", want, got)
		}
	}
}

func writeConfig(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
