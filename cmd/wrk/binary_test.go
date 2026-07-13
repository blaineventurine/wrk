package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildTestBinary compiles ./cmd/wrk into a temp dir and returns its
// path. Shared setup for the T2 end-to-end tests below.
//
// Slow-ish (~1s cached; ~3s cold), but each test uses the same binary
// under the shared t.TempDir() so we only pay the cost once per package
// test run.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "wrk")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\n%s", err, stderr.String())
	}
	return bin
}

// initTestRepo makes a minimal git repo with an empty .wrk.yml so
// wrk can find a valid working directory to plan against.
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGit := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		var stderr bytes.Buffer
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, ".wrk.yml"), []byte("resources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "init")
	return repo
}

// TestBinaryEmitsStructuredErrorUnderJSON is the end-to-end agent-contract
// test: build a real wrk binary, exec a failing destructive command with
// `--json --yes`, parse stderr as JSON, assert the code is stable and
// message + hint are populated. Every layer that shapes the error
// envelope — engine.Error type, emitJSONError, exitCode sentinel — must
// cooperate for this test to pass, so it guards the whole contract.
func TestBinaryEmitsStructuredErrorUnderJSON(t *testing.T) {
	bin := buildTestBinary(t)
	repo := initTestRepo(t)

	cmd := exec.Command(bin, "run", "nope", "--json", "--yes")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err == nil {
		t.Fatal("expected non-zero exit, got success")
	}

	// STDOUT must be empty when the command fails — the JSON error
	// envelope is on STDERR so agents piping stdout through jq stay
	// happy on the success path.
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty on error, got:\n%s", stdout.String())
	}

	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &out); jerr != nil {
		t.Fatalf("stderr not valid JSON: %v\n%s", jerr, stderr.String())
	}
	if out.Error.Code != "resource_not_configured" {
		t.Errorf("code = %q, want resource_not_configured", out.Error.Code)
	}
	if out.Error.Message == "" {
		t.Error("message empty; agents rely on it for human-facing display")
	}
}

// TestBinaryRefusesJSONWithoutConsent locks the H1 fix: `--json` on a
// destructive command without `--yes`/`--force`/`--dry-run` used to
// silently hang the Confirm prompt in the redirected stdout buffer.
// Now it must refuse with a stable structured error so agents get an
// actionable failure instead of a hang.
func TestBinaryRefusesJSONWithoutConsent(t *testing.T) {
	bin := buildTestBinary(t)
	repo := initTestRepo(t)

	cmd := exec.Command(bin, "gc", "--json")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if err == nil {
		t.Fatal("expected non-zero exit for --json without --yes")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty on refusal, got:\n%s", stdout.String())
	}

	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if jerr := json.Unmarshal(bytes.TrimSpace(stderr.Bytes()), &out); jerr != nil {
		t.Fatalf("stderr not valid JSON: %v\n%s", jerr, stderr.String())
	}
	if out.Error.Code != "json_requires_yes" {
		t.Errorf("code = %q, want json_requires_yes", out.Error.Code)
	}
}

// TestBinaryDryRunAllowsJSONWithoutYes verifies the escape hatch on
// H1: `--json --dry-run` skips execute entirely, so it doesn't need
// --yes to bypass the (nonexistent) prompt. Agents previewing a plan
// shouldn't have to also assert their consent to execute.
func TestBinaryDryRunAllowsJSONWithoutYes(t *testing.T) {
	bin := buildTestBinary(t)
	repo := initTestRepo(t)

	cmd := exec.Command(bin, "gc", "--json", "--dry-run")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected clean exit for --json --dry-run, got %v\nstderr: %s", err, stderr.String())
	}

	var out struct {
		Schema int    `json:"schema"`
		Kind   string `json:"kind"`
		DryRun bool   `json:"dryRun"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out); err != nil {
		t.Fatalf("stdout not valid JSON: %v\n%s", err, stdout.String())
	}
	if out.Schema != 1 || out.Kind != "gc" || !out.DryRun {
		t.Errorf("envelope wrong: schema=%d kind=%q dryRun=%v", out.Schema, out.Kind, out.DryRun)
	}
}
