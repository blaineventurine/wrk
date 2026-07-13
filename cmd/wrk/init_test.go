package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitFlagsYesRegistered pins the flag wiring for `wrk init --yes`
// / `-y`. --yes is only meaningful under --force against an existing
// .wrk.yml, but the flag must exist so users can pass it without
// tripping an unknown-flag error.
func TestInitFlagsYesRegistered(t *testing.T) {
	long := initCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on initCmd")
	}
	short := initCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on initCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to initYes)")
	}
}

// TestInitFlagsForceRegistered pins that `wrk init --force` / `-f`
// is still wired.
func TestInitFlagsForceRegistered(t *testing.T) {
	long := initCmd.Flags().Lookup("force")
	if long == nil {
		t.Fatal("--force flag not registered on initCmd")
	}
	short := initCmd.Flags().ShorthandLookup("f")
	if short == nil {
		t.Fatal("-f shorthand not registered on initCmd")
	}
	if long != short {
		t.Fatal("--force and -f must be the same flag (bound to initForce)")
	}
}

// TestInitForceOnExistingRefusesWithoutYes pins the destructive-
// action gate: `wrk init --force` against a repo that already has a
// .wrk.yml MUST print the "Overwriting" preview and then refuse
// without --yes on a non-TTY. The pre-existing config bytes MUST
// survive the refusal — otherwise the prompt is a lie.
func TestInitForceOnExistingRefusesWithoutYes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")
	original := "resources: []  # deliberate marker so we can detect overwrite\n"
	writeFile(t, filepath.Join(repo, ".wrk.yml"), original)

	code, stdout, stderr := runWrk(t, repo, "init", "--force")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Overwriting") {
		t.Errorf("stdout should announce the overwrite before refusing, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr should mention --yes so users know the fix, got: %q", stderr)
	}

	// The pre-existing config MUST survive a refusal — the whole
	// point of the prompt is to protect it.
	got, err := readFile(t, filepath.Join(repo, ".wrk.yml"))
	if err != nil {
		t.Fatalf("read post-refusal .wrk.yml: %v", err)
	}
	if got != original {
		t.Fatalf("refused init still overwrote .wrk.yml:\ngot:\n%s\nwant:\n%s", got, original)
	}
}

// TestInitForceYesOverwritesSilently pins the happy path for a
// permitted overwrite: --force AND --yes together proceed without
// prompting, and the new file is written. This is what scripts and
// CI will invoke; anything less than a silent success would break
// them.
func TestInitForceYesOverwritesSilently(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")
	writeFile(t, filepath.Join(repo, ".wrk.yml"),
		"resources: []  # placeholder that init should replace\n")

	code, stdout, stderr := runWrk(t, repo, "init", "--force", "--yes")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	// The overwrite banner should still print — it's informational.
	if !strings.Contains(stdout, "Overwriting") {
		t.Errorf("stdout should announce the overwrite even when silent, got:\n%s", stdout)
	}
	// And the new file must have been written (the detection line
	// mentions the env fixture we planted).
	if !strings.Contains(stdout, "env") {
		t.Errorf("stdout should mention the 'env' detection after successful overwrite, got:\n%s", stdout)
	}

	// The .wrk.yml on disk should no longer be the placeholder.
	got, err := readFile(t, filepath.Join(repo, ".wrk.yml"))
	if err != nil {
		t.Fatalf("read post-overwrite .wrk.yml: %v", err)
	}
	if strings.Contains(got, "placeholder that init should replace") {
		t.Fatalf(".wrk.yml still contains the placeholder — overwrite did not run:\n%s", got)
	}
}

// TestInitFreshRepoNoConfirmPrompt pins that a plain `wrk init` in
// a repo WITHOUT a pre-existing .wrk.yml never asks for consent.
// There is nothing destructive about writing the file for the first
// time, so a spurious prompt (or non-TTY refusal) would block every
// first-time user unnecessarily.
func TestInitFreshRepoNoConfirmPrompt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")

	code, stdout, stderr := runWrk(t, repo, "init")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Overwriting") {
		t.Errorf("fresh init should not print 'Overwriting'; got:\n%s", stdout)
	}
	if strings.Contains(stderr, "--yes") {
		t.Errorf("fresh init should not refuse for --yes; got:\n%s", stderr)
	}
}

// TestInitForceOnSymlinkedConfigPrompts pins the Lstat-not-Stat fix:
// a `.wrk.yml` that is a SYMLINK to a broken (or missing) target used
// to slip past the existence check — `os.Stat` follows the link, sees
// "not exist," and skipped the "Overwriting" prompt. The next
// WriteFile then silently replaced the symlink with a regular file
// containing the freshly-generated config, quietly severing the user's
// deliberate indirection.
//
// The fix uses `os.Lstat`, which reports the link itself and triggers
// the prompt. Two branches: without --yes (non-TTY refuses); with
// --yes (overwrite proceeds and the link is replaced by a real file).
func TestInitForceOnSymlinkedConfigPrompts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".env.example"), "")

	// Symlink .wrk.yml -> a target that does NOT exist. Stat sees
	// "not exist" and would skip the prompt; Lstat sees the link.
	brokenTarget := filepath.Join(repo, "config-elsewhere.yml")
	link := filepath.Join(repo, ".wrk.yml")
	if err := os.Symlink(brokenTarget, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// Without --yes on a non-TTY, the prompt refuses and the symlink
	// survives unchanged.
	code, stdout, stderr := runWrk(t, repo, "init", "--force")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Overwriting") {
		t.Errorf("stdout should announce the overwrite for the symlink, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr should mention --yes so users know the fix, got: %q", stderr)
	}

	// The symlink MUST still be a symlink — refusal preserves state.
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat after refusal: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".wrk.yml is no longer a symlink after refusal (mode=%v)", info.Mode())
	}

	// With --yes, the overwrite proceeds. Post-condition: .wrk.yml is
	// now a regular file (the symlink was replaced by WriteFile) with
	// engine.Init's generated content.
	code, stdout, stderr = runWrk(t, repo, "init", "--force", "--yes")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	info, err = os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat after overwrite: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".wrk.yml is still a symlink after overwrite (mode=%v); WriteFile should have replaced it", info.Mode())
	}
	if !info.Mode().IsRegular() {
		t.Fatalf(".wrk.yml is not a regular file after overwrite (mode=%v)", info.Mode())
	}
}

// readFile is a t.Helper wrapper around os.ReadFile that returns the
// content as a string — the CLI init tests compare against small
// YAML snippets that are easier to eyeball as strings than []byte.
func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// initJSONEnvelope mirrors engine.MarshalInitJSON's wire shape. Shared
// across the `wrk init --json` end-to-end tests so the assertions and
// the format can't drift apart silently.
type initJSONEnvelope struct {
	Schema int    `json:"schema"`
	Kind   string `json:"kind"`
	DryRun bool   `json:"dryRun"`
	Plan   struct {
		Path     string   `json:"path"`
		Detected []string `json:"detected"`
		Exists   bool     `json:"exists"`
		Content  string   `json:"content"`
	} `json:"plan"`
	Result *struct {
		Wrote    bool     `json:"wrote"`
		Warnings []string `json:"warnings"`
	} `json:"result"`
}

// TestInitJSONDryRunPreviewsWithoutWriting pins the `wrk init --json
// --dry-run` contract: the envelope carries the full generated config
// in plan.content, plan.detected reflects the project fixtures
// (package.json + yarn.lock → a node_modules resource), the `result`
// key is absent, and NO file lands on disk. Strict json.Unmarshal on
// raw stdout doubles as the purity check — the human preview must not
// leak into the machine-readable stream.
func TestInitJSONDryRunPreviewsWithoutWriting(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, "package.json"), "{}\n")
	writeFile(t, filepath.Join(repo, "yarn.lock"), "")

	exit, stdout, stderr := runWrk(t, repo, "init", "--json", "--dry-run")
	if exit != 0 {
		t.Fatalf("init --json --dry-run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			exit, stdout, stderr)
	}

	var out initJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not a single pure JSON object: %v\n%s", err, stdout)
	}
	if out.Schema != 1 || out.Kind != "init" {
		t.Errorf("envelope wrong: schema=%d kind=%q, want 1/init", out.Schema, out.Kind)
	}
	if !out.DryRun {
		t.Error("dryRun = false, want true")
	}
	if want := filepath.Join(repo, ".wrk.yml"); out.Plan.Path != want {
		t.Errorf("plan.path = %q, want %q", out.Plan.Path, want)
	}
	if out.Plan.Exists {
		t.Error("plan.exists = true in a repo without .wrk.yml, want false")
	}

	foundNode := false
	for _, d := range out.Plan.Detected {
		if d == "node_modules" {
			foundNode = true
			break
		}
	}
	if !foundNode {
		t.Errorf("plan.detected = %q, want it to contain %q (package.json + yarn.lock fixture)",
			out.Plan.Detected, "node_modules")
	}
	if out.Plan.Content == "" {
		t.Error("plan.content empty on --dry-run, want the full generated config")
	}
	if !strings.Contains(out.Plan.Content, "node_modules") {
		t.Errorf("plan.content should carry a node_modules resource, got:\n%s",
			out.Plan.Content)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("re-decode into map: %v", err)
	}
	if _, ok := raw["result"]; ok {
		t.Errorf("result key present on --dry-run, must be omitted:\n%s", stdout)
	}

	if _, err := os.Lstat(filepath.Join(repo, ".wrk.yml")); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote .wrk.yml to disk (lstat err=%v)", err)
	}
}

// TestInitJSONWriteRefuseForceYesMatrix walks the full `wrk init
// --json` consent state machine against ONE repo, in order:
//
//  1. fresh repo            → writes the file, result.wrote true.
//  2. exists, no --force    → exit 2, "already exists" envelope,
//     stdout empty, user file untouched.
//  3. exists, --force only  → exit 2, code json_requires_yes (a
//     --json caller has no TTY to answer the overwrite prompt),
//     user file untouched.
//  4. exists, --force --yes → overwrites; plan.exists true,
//     result.wrote true.
//
// Steps 2 and 3 plant marker content so a regression that writes
// despite refusing flips the byte-level survival assertions red, not
// just the exit codes.
func TestInitJSONWriteRefuseForceYesMatrix(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, "package.json"), "{}\n")
	writeFile(t, filepath.Join(repo, "yarn.lock"), "")
	target := filepath.Join(repo, ".wrk.yml")

	// Step 1: fresh write.
	exit, stdout, stderr := runWrk(t, repo, "init", "--json")
	if exit != 0 {
		t.Fatalf("step 1: init --json exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			exit, stdout, stderr)
	}
	var out initJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("step 1: stdout is not a single pure JSON object: %v\n%s", err, stdout)
	}
	if out.Schema != 1 || out.Kind != "init" || out.DryRun {
		t.Errorf("step 1: envelope wrong: schema=%d kind=%q dryRun=%v",
			out.Schema, out.Kind, out.DryRun)
	}
	if out.Plan.Exists {
		t.Error("step 1: plan.exists = true on a fresh repo, want false")
	}
	if out.Plan.Content != "" {
		t.Error("step 1: plan.content populated on a real write — content is a dry-run-only field")
	}
	if out.Result == nil {
		t.Fatalf("step 1: result missing on a real write:\n%s", stdout)
	}
	if !out.Result.Wrote {
		t.Error("step 1: result.wrote = false, want true")
	}
	if out.Result.Warnings == nil {
		t.Error("step 1: result.warnings is null, want [] (never-null array contract)")
	}
	got, err := readFile(t, target)
	if err != nil {
		t.Fatalf("step 1: .wrk.yml not written: %v", err)
	}
	if !strings.Contains(got, "node_modules") {
		t.Errorf("step 1: written config lacks the detected node_modules resource:\n%s", got)
	}

	// Plant marker content so steps 2 and 3 can prove the refusals
	// leave user edits untouched.
	marker := "resources: []  # user-edited marker\n"
	writeFile(t, target, marker)

	// Step 2: exists, no --force.
	exit, stdout, stderr = runWrk(t, repo, "init", "--json")
	if exit != 2 {
		t.Fatalf("step 2: exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("step 2: stdout must be empty on --json failure, got:\n%s", stdout)
	}
	_, msg, _ := decodeErrorEnvelope(t, bytes.NewBufferString(stderr))
	if !strings.Contains(msg, "already exists") {
		t.Errorf("step 2: error message = %q, want it to name %q", msg, "already exists")
	}
	if got, _ := readFile(t, target); got != marker {
		t.Fatalf("step 2: refusal still rewrote .wrk.yml:\ngot:\n%s\nwant:\n%s", got, marker)
	}

	// Step 3: exists, --force but no --yes — --json cannot answer the
	// interactive overwrite prompt, so it must refuse with the stable
	// json_requires_yes code instead of hanging or writing.
	exit, stdout, stderr = runWrk(t, repo, "init", "--json", "--force")
	if exit != 2 {
		t.Fatalf("step 3: exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("step 3: stdout must be empty on --json failure, got:\n%s", stdout)
	}
	code, _, _ := decodeErrorEnvelope(t, bytes.NewBufferString(stderr))
	if code != "json_requires_yes" {
		t.Errorf("step 3: error.code = %q, want %q", code, "json_requires_yes")
	}
	if got, _ := readFile(t, target); got != marker {
		t.Fatalf("step 3: refusal still rewrote .wrk.yml:\ngot:\n%s\nwant:\n%s", got, marker)
	}

	// Step 4: exists, --force --yes — explicit consent, overwrite runs.
	exit, stdout, stderr = runWrk(t, repo, "init", "--json", "--force", "--yes")
	if exit != 0 {
		t.Fatalf("step 4: exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	out = initJSONEnvelope{}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("step 4: stdout is not a single pure JSON object: %v\n%s", err, stdout)
	}
	if !out.Plan.Exists {
		t.Error("step 4: plan.exists = false against a pre-existing config, want true")
	}
	if out.Result == nil || !out.Result.Wrote {
		t.Fatalf("step 4: result.wrote should be true after a consented overwrite:\n%s", stdout)
	}
	got, err = readFile(t, target)
	if err != nil {
		t.Fatalf("step 4: read .wrk.yml: %v", err)
	}
	if strings.Contains(got, "user-edited marker") {
		t.Fatalf("step 4: marker survived a consented overwrite — file was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "node_modules") {
		t.Errorf("step 4: rewritten config lacks the detected node_modules resource:\n%s", got)
	}
}
