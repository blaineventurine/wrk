package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
	"github.com/blaineventurine/wrk/internal/repository"
)

// TestHumanSizeClamps pins the M3 defensive clamp: an input large
// enough to walk past the last defined SI suffix ('E') must fall back
// to that suffix instead of indexing past the "KMGTPE" string. No real
// filesystem produces such a size, but the arithmetic path allows it,
// and a panic here would take down `wrk list --size`.
func TestHumanSizeClamps(t *testing.T) {
	// math.MaxInt64 bytes is a lot more than an exabyte; without the
	// clamp, humanSize would step exp to 6+ and index out of range.
	got := engine.HumanSize(math.MaxInt64)
	if got == "" {
		t.Fatalf("humanSize(MaxInt64) returned empty string")
	}
	// The last defined suffix is 'E' — verify we clamped to it.
	if got[len(got)-2:] != "EB" {
		t.Fatalf("humanSize(MaxInt64) = %q, want …EB (clamped to exabytes)", got)
	}
}

// TestHumanSizeSpotChecks fixes a few well-known values so a regression
// in the loop itself is caught alongside the clamp.
func TestHumanSizeSpotChecks(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1 KB"},
		{-1, "-"},
	}
	for _, c := range cases {
		if got := engine.HumanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPrintListJSONEmitsSchemaEnvelope pins the JSON envelope +
// trailing-newline contract. Building a live git-backed repo
// in-process is the cheapest way to exercise printListJSON's real
// engine.MarshalListJSON path without spawning the compiled binary.
func TestPrintListJSONEmitsSchemaEnvelope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir := freshGitRepo(t)
	storage := storagePath(repoDir)
	writeFile(t, filepath.Join(repoDir, ".wrk.yml"),
		"resources:\n  - name: env\n    path: .env\n")

	repo, err := repository.Detect(repoDir, repository.Auto)
	if err != nil {
		t.Fatalf("repository.Detect: %v", err)
	}

	var buf bytes.Buffer
	if err := printListJSON(&buf, repo,
		engine.Options{StorageRoot: storage, Stdout: &buf}, false); err != nil {
		t.Fatalf("printListJSON: %v", err)
	}

	// Trailing newline for shell-friendliness — mirrors the status
	// path (see TestPrintStatusJSONEmitsSchemaEnvelope) and lets users
	// pipe through `jq` without a warning.
	if !bytes.HasSuffix(buf.Bytes(), []byte("\n")) {
		t.Errorf("output missing trailing newline:\n%s", buf.String())
	}

	var out struct {
		Schema    int    `json:"schema"`
		Kind      string `json:"kind"`
		Root      string `json:"root"`
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out); err != nil {
		t.Fatalf("invalid JSON:\n%s\n%v", buf.String(), err)
	}
	if out.Schema != 1 || out.Kind != "list" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
	if out.Root != repo.Root {
		t.Errorf("root: got %q, want %q", out.Root, repo.Root)
	}
	if len(out.Resources) != 1 || out.Resources[0].Name != "env" {
		t.Errorf("expected 1 resource named env, got %+v", out.Resources)
	}
}

// TestPrintListJSONPropagatesWriterError pins the failure path: a
// downstream write error MUST surface so the CLI exits 2 rather than
// silently succeeding on a broken stdout. Reuses failingWriter from
// status_test.go (package-scoped).
func TestPrintListJSONPropagatesWriterError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir := freshGitRepo(t)
	writeFile(t, filepath.Join(repoDir, ".wrk.yml"), "resources: []\n")

	repo, err := repository.Detect(repoDir, repository.Auto)
	if err != nil {
		t.Fatalf("repository.Detect: %v", err)
	}

	err = printListJSON(&failingWriter{}, repo,
		engine.Options{StorageRoot: storagePath(repoDir)}, false)
	if err == nil {
		t.Fatal("expected error from failing writer, got nil")
	}
}

// TestListJSONFlagEmitsSchemaEnvelope is the end-to-end guard: driving
// the compiled binary through `list --json` MUST print the schema
// envelope + a resource row to stdout, exit 0, and not leak the human
// tabular header.
func TestListJSONFlagEmitsSchemaEnvelope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := freshGitRepo(t)
	storage := storagePath(repo)
	writeFile(t, filepath.Join(repo, ".wrk.yml"),
		"resources:\n  - name: env\n    path: .env\n")

	code, stdout, stderr := runWrk(t, repo, "--storage", storage, "list", "--json")
	if code != 0 {
		t.Fatalf("list --json exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			code, stdout, stderr)
	}
	// Human-mode column header MUST NOT appear in JSON output.
	if strings.Contains(stdout, "RESOURCE\tPATH") ||
		strings.Contains(stdout, "SHARED PATH") {
		t.Errorf("tabular header leaked into JSON output:\n%s", stdout)
	}

	var out struct {
		Schema    int    `json:"schema"`
		Kind      string `json:"kind"`
		Resources []struct {
			Name string `json:"name"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		t.Fatalf("invalid JSON output:\n%s\n%v", stdout, err)
	}
	if out.Schema != 1 || out.Kind != "list" {
		t.Errorf("envelope wrong: schema=%d kind=%q", out.Schema, out.Kind)
	}
	if len(out.Resources) != 1 || out.Resources[0].Name != "env" {
		t.Errorf("expected env resource, got %+v", out.Resources)
	}
}
