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

// TestNewFlagBaseRegistered pins that `wrk new` exposes `--base` with
// an empty default: a user typing `wrk new feature --base main` gets
// their argument threaded through to engine.NewWorkspace, and a user
// who omits the flag gets the legacy behaviour (empty base means
// "fork off the invoking worktree's HEAD/@"). A regression that
// swapped StringVar for a different type or moved the flag registration
// off newCmd would trip this test at the flag lookup.
func TestNewFlagBaseRegistered(t *testing.T) {
	f := newCmd.Flags().Lookup("base")
	if f == nil {
		t.Fatal("--base flag not registered on `wrk new`")
	}
	if f.DefValue != "" {
		t.Errorf("--base default = %q, want empty (empty means: use current HEAD/@)",
			f.DefValue)
	}
}

// newJSONEnvelope mirrors engine.MarshalNewJSON's wire shape. Shared
// across the `wrk new --json` end-to-end tests so the assertions and
// the format can't drift apart silently.
type newJSONEnvelope struct {
	Schema int    `json:"schema"`
	Kind   string `json:"kind"`
	DryRun bool   `json:"dryRun"`
	Plan   struct {
		Destination         string   `json:"destination"`
		PrimaryActionCount  int      `json:"primaryActionCount"`
		PrimaryDescriptions []string `json:"primaryDescriptions"`
	} `json:"plan"`
	Result *struct {
		Created       bool     `json:"created"`
		WorkspaceRoot string   `json:"workspaceRoot"`
		Warnings      []string `json:"warnings"`
	} `json:"result"`
}

// TestNewJSONEmitsPureEnvelopeWithLinkWarnings pins the `wrk new
// --json` happy-path contract end to end:
//
//   - stdout is EXACTLY one JSON object — the strict json.Unmarshal on
//     the raw stream (no trimming) fails on any non-JSON prefix. This
//     is the passthroughTo regression guard: before the fix,
//     `git worktree add`'s "HEAD is now at ..." checkout notice leaked
//     onto stdout ahead of the envelope.
//   - kind/schema identify the payload; plan.destination is absolute
//     and equals result.workspaceRoot; result.created is true.
//   - the plain-text link chatter (primary link + the new workspace's
//     provisioning link) is re-emitted in result.warnings, so no
//     information the human path prints is lost to a --json caller.
//
// The on-disk assertions (workspace dir exists at plan.destination,
// .env inside it is a symlink) keep the envelope honest: it must
// describe what actually happened, not merely echo its inputs.
func TestNewJSONEmitsPureEnvelopeWithLinkWarnings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"),
		"resources:\n  - name: env\n    path: .env\n")
	gitCommitAll(t, repo, "init")
	// Untracked real .env: forces the primary link (move + symlink,
	// printed as chatter) and seeds shared storage so the new
	// workspace's second link plans a symlink action.
	writeFile(t, filepath.Join(repo, ".env"), "SECRET=1\n")

	sibling := filepath.Join(filepath.Dir(repo), "feature-json")
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "worktree", "remove", "-f", sibling).Run()
		_ = os.RemoveAll(sibling)
	})

	exit, stdout, stderr := runWrk(t, repo,
		"--storage", storagePath(repo), "new", "--json", "feature-json")
	if exit != 0 {
		t.Fatalf("new --json exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			exit, stdout, stderr)
	}

	// Targeted regression probe with a readable failure message: the
	// captured "HEAD is now at ..." checkout notice legitimately lives
	// INSIDE the envelope's warnings array, so the leak signature is a
	// non-"{" PREFIX ahead of the JSON, not the substring itself. The
	// strict Unmarshal below is the load-bearing purity check (it
	// rejects any non-whitespace prefix or suffix).
	if !strings.HasPrefix(stdout, "{") {
		t.Errorf("non-JSON prefix on stdout (passthroughTo regression — git chatter leaked?):\n%s", stdout)
	}

	var out newJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not a single pure JSON object: %v\n%s", err, stdout)
	}
	if out.Schema != 1 || out.Kind != "new" {
		t.Errorf("envelope wrong: schema=%d kind=%q, want 1/new", out.Schema, out.Kind)
	}
	if out.DryRun {
		t.Error("dryRun = true on a real run, want false")
	}
	if !filepath.IsAbs(out.Plan.Destination) {
		t.Errorf("plan.destination = %q, want absolute path", out.Plan.Destination)
	}

	if out.Result == nil {
		t.Fatalf("result missing on a real (non-dry-run) invocation:\n%s", stdout)
	}
	if !out.Result.Created {
		t.Error("result.created = false, want true")
	}
	if out.Result.WorkspaceRoot != out.Plan.Destination {
		t.Errorf("result.workspaceRoot = %q, plan.destination = %q — must match",
			out.Result.WorkspaceRoot, out.Plan.Destination)
	}

	// The link chatter must surface in warnings, not vanish.
	foundLink := false
	for _, w := range out.Result.Warnings {
		if strings.Contains(w, "link ") && strings.Contains(w, ".env") {
			foundLink = true
			break
		}
	}
	if !foundLink {
		t.Errorf("warnings should carry the plan's link lines, got %q",
			out.Result.Warnings)
	}

	// Envelope honesty: the destination it names really is a
	// provisioned workspace.
	if info, err := os.Stat(out.Plan.Destination); err != nil || !info.IsDir() {
		t.Fatalf("plan.destination %q is not a directory on disk (err=%v)",
			out.Plan.Destination, err)
	}
	if _, err := os.Stat(filepath.Join(out.Plan.Destination, ".wrk.yml")); err != nil {
		t.Errorf(".wrk.yml missing in created workspace: %v", err)
	}
	envInfo, err := os.Lstat(filepath.Join(out.Plan.Destination, ".env"))
	if err != nil {
		t.Fatalf(".env missing in created workspace: %v", err)
	}
	if envInfo.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".env in the new workspace is not a symlink (mode=%v) — provisioning link did not run",
			envInfo.Mode())
	}
}

// TestNewJSONDryRunOmitsResultAndCreatesNothing pins the --json
// --dry-run contract: dryRun is true, the `result` key is ABSENT
// (nothing was attempted, so nothing may claim to have happened), and
// the destination does not appear on disk. The human dry-run banner
// ("Would create workspace ...") must not pollute the JSON stream —
// the strict Unmarshal on raw stdout enforces that.
func TestNewJSONDryRunOmitsResultAndCreatesNothing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	exit, stdout, stderr := runWrk(t, repo,
		"--storage", storagePath(repo), "new", "--json", "--dry-run", "preview")
	if exit != 0 {
		t.Fatalf("new --json --dry-run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			exit, stdout, stderr)
	}

	var out newJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not a single pure JSON object: %v\n%s", err, stdout)
	}
	if out.Schema != 1 || out.Kind != "new" {
		t.Errorf("envelope wrong: schema=%d kind=%q, want 1/new", out.Schema, out.Kind)
	}
	if !out.DryRun {
		t.Error("dryRun = false, want true")
	}
	if !filepath.IsAbs(out.Plan.Destination) {
		t.Errorf("plan.destination = %q, want absolute path", out.Plan.Destination)
	}

	// Key-level absence, not just a nil pointer: `"result": null`
	// would decode to nil too, but the contract is that the key is
	// omitted entirely.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("re-decode into map: %v", err)
	}
	if _, ok := raw["result"]; ok {
		t.Errorf("result key present on --dry-run, must be omitted:\n%s", stdout)
	}

	if _, err := os.Stat(out.Plan.Destination); !os.IsNotExist(err) {
		t.Errorf("--dry-run created %q on disk (stat err=%v)", out.Plan.Destination, err)
	}
}

// TestNewJSONDestinationExistsEmitsErrorEnvelope pins the --json
// failure contract: a pre-existing destination refuses with exit 2,
// stdout stays completely EMPTY (a partial envelope would poison
// downstream parsers), and stderr carries exactly one structured
// error envelope whose message names "already exists". The
// pre-existing directory's content survives byte-for-byte — the
// refusal must fire before git or the executor touch anything.
func TestNewJSONDestinationExistsEmitsErrorEnvelope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := freshGitRepo(t)
	writeFile(t, filepath.Join(repo, ".wrk.yml"), "resources: []\n")
	gitCommitAll(t, repo, "init")

	taken := filepath.Join(filepath.Dir(repo), "taken")
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := "user data that must survive\n"
	writeFile(t, filepath.Join(taken, "keep.txt"), marker)

	exit, stdout, stderr := runWrk(t, repo,
		"--storage", storagePath(repo), "new", "--json", "taken")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty on --json failure, got:\n%s", stdout)
	}

	_, msg, _ := decodeErrorEnvelope(t, bytes.NewBufferString(stderr))
	if !strings.Contains(msg, "already exists") {
		t.Errorf("error message = %q, want it to name %q", msg, "already exists")
	}

	got, err := os.ReadFile(filepath.Join(taken, "keep.txt"))
	if err != nil {
		t.Fatalf("pre-existing file gone after refusal: %v", err)
	}
	if string(got) != marker {
		t.Errorf("pre-existing content changed after refusal:\ngot:  %q\nwant: %q",
			got, marker)
	}
}
