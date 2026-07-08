package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectNodeYarn(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "yarn.lock"))

	got := detect(dir)

	if len(got) != 1 || got[0].kind != "node-yarn" {
		t.Fatalf("detect = %+v, want [{kind:node-yarn}]", got)
	}
}

func TestDetectMultipleProjects(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, ".env.example"))
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "pnpm-lock.yaml"))
	touch(t, filepath.Join(dir, "Gemfile"))

	got := detect(dir)

	kinds := []string{}
	for _, d := range got {
		kinds = append(kinds, d.kind)
	}

	want := []string{"env", "node-pnpm", "bundler"}
	if !equalSlice(kinds, want) {
		t.Fatalf("detected kinds = %v, want %v", kinds, want)
	}
}

func TestDetectPythonPrefersLockfile(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "pyproject.toml"))
	touch(t, filepath.Join(dir, "uv.lock"))
	touch(t, filepath.Join(dir, "requirements.txt")) // should be ignored when uv.lock is present

	got := detect(dir)

	if len(got) != 1 || got[0].kind != "python-uv" {
		t.Fatalf("detect = %+v, want python-uv only", got)
	}
}

func TestDetectNothing(t *testing.T) {
	dir := t.TempDir()

	if got := detect(dir); len(got) != 0 {
		t.Fatalf("detect = %+v, want empty", got)
	}
}

func TestInitWritesAndLoads(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "yarn.lock"))

	var out bytes.Buffer
	err := Init(InitOptions{Root: dir, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}

	// File must exist and be valid wrk config.
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}
	if len(cfg.Resources) == 0 {
		t.Fatal("expected at least one resource in generated config")
	}

	// Output must mention "wrk link".
	if !strings.Contains(out.String(), "wrk link") {
		t.Errorf("output = %q, want mention of 'wrk link'", out.String())
	}
}

func TestInitRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".wrk.yml"), "resources: []\n")

	err := Init(InitOptions{Root: dir, Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected error when .wrk.yml already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want 'already exists'", err.Error())
	}
}

func TestInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".wrk.yml"), "resources: []\n")
	touch(t, filepath.Join(dir, "Gemfile"))

	err := Init(InitOptions{Root: dir, Force: true, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("expected --force to succeed, got %v", err)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}

	// Should now have a bundler resource, not an empty one.
	if len(cfg.Resources) == 0 {
		t.Fatal("expected bundler resource after force-overwrite")
	}
	if cfg.Resources[0].Name != "bundler" {
		t.Errorf("Resources[0].Name = %q, want bundler", cfg.Resources[0].Name)
	}
}

func TestInitDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "yarn.lock"))

	var out bytes.Buffer
	err := Init(InitOptions{Root: dir, DryRun: true, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}

	// File must NOT have been written.
	if _, err := os.Stat(filepath.Join(dir, ".wrk.yml")); !os.IsNotExist(err) {
		t.Fatal("dry-run should not write .wrk.yml")
	}

	// Output must contain YAML content.
	if !strings.Contains(out.String(), "node_modules") {
		t.Errorf("dry-run output = %q, want YAML content", out.String())
	}
}

func TestInitEmptyDirProducesTemplate(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	err := Init(InitOptions{Root: dir, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}

	// Even with no detections the file must be valid (resources: []).
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("empty template failed to load: %v", err)
	}
	if len(cfg.Resources) != 0 {
		t.Fatalf("expected 0 resources in empty template, got %d", len(cfg.Resources))
	}

	// Output should mention the template.
	if !strings.Contains(out.String(), "template") {
		t.Errorf("output = %q, want mention of 'template'", out.String())
	}
}

func TestInitMonorepoDetected(t *testing.T) {
	dir := t.TempDir()

	// Write a package.json with workspaces field.
	writeFile(t, filepath.Join(dir, "package.json"), `{
		"name": "monorepo",
		"workspaces": ["packages/*", "apps/*"]
	}`)
	touch(t, filepath.Join(dir, "yarn.lock"))

	var out bytes.Buffer
	err := Init(InitOptions{Root: dir, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(dir, ".wrk.yml"))
	if err != nil {
		t.Fatal(err)
	}

	// Monorepo comment must appear.
	if !strings.Contains(string(content), "monorepo") {
		t.Errorf("expected monorepo comment in output, got:\n%s", content)
	}

	// Detected workspace patterns must be mentioned.
	if !strings.Contains(string(content), "packages/*") {
		t.Errorf("expected workspace patterns in output, got:\n%s", content)
	}
}

func TestInitMultipleDetections(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, ".env.example"))
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "yarn.lock"))
	touch(t, filepath.Join(dir, "Gemfile"))

	var out bytes.Buffer
	err := Init(InitOptions{Root: dir, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}

	// Expect env + node + bundler.
	if len(cfg.Resources) != 3 {
		t.Fatalf("expected 3 resources, got %d", len(cfg.Resources))
	}

	names := map[string]bool{}
	for _, r := range cfg.Resources {
		names[r.Name] = true
	}

	for _, want := range []string{"env", "node", "bundler"} {
		if !names[want] {
			t.Errorf("expected resource %q, got %v", want, names)
		}
	}
}

func TestDetectNodePnpm(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "pnpm-lock.yaml"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "node-pnpm" {
		t.Fatalf("detect = %+v, want [{node-pnpm}]", got)
	}
}

func TestDetectNodeNpm(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))
	touch(t, filepath.Join(dir, "package-lock.json"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "node-npm" {
		t.Fatalf("detect = %+v, want [{node-npm}]", got)
	}
}

func TestDetectNodeNoLock(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "package.json"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "node-nolock" {
		t.Fatalf("detect = %+v, want [{node-nolock}]", got)
	}
}

func TestDetectBundler(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Gemfile"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "bundler" {
		t.Fatalf("detect = %+v, want [{bundler}]", got)
	}
}

func TestDetectPythonUV(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "pyproject.toml"))
	touch(t, filepath.Join(dir, "uv.lock"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "python-uv" {
		t.Fatalf("detect = %+v, want [{python-uv}]", got)
	}
}

func TestDetectPythonPoetry(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "pyproject.toml"))
	touch(t, filepath.Join(dir, "poetry.lock"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "python-poetry" {
		t.Fatalf("detect = %+v, want [{python-poetry}]", got)
	}
}

func TestDetectPythonPipenv(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Pipfile.lock"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "python-pipenv" {
		t.Fatalf("detect = %+v, want [{python-pipenv}]", got)
	}
}

func TestDetectPythonPip(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "requirements.txt"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "python-pip" {
		t.Fatalf("detect = %+v, want [{python-pip}]", got)
	}
}

func TestDetectPythonPrefersUVOverRequirements(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "pyproject.toml"))
	touch(t, filepath.Join(dir, "uv.lock"))
	touch(t, filepath.Join(dir, "requirements.txt"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "python-uv" {
		t.Fatalf("detect = %+v, want [{python-uv}] only", got)
	}
}

func TestDetectCargo(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "Cargo.toml"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "cargo-commented" {
		t.Fatalf("detect = %+v, want [{cargo-commented}]", got)
	}
}

func TestDetectEnvExample(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, ".env.example"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "env" {
		t.Fatalf("detect = %+v, want [{env}]", got)
	}
}

func TestDetectEnvSample(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, ".env.sample"))

	got := detect(dir)
	if len(got) != 1 || got[0].kind != "env" {
		t.Fatalf("detect = %+v, want [{env}]", got)
	}
}

func TestDetectMonorepoWorkspaces(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{
		"name": "monorepo",
		"workspaces": ["packages/*", "apps/*"]
	}`)
	touch(t, filepath.Join(dir, "yarn.lock"))

	got := detect(dir)

	kinds := make([]string, 0, len(got))
	for _, d := range got {
		kinds = append(kinds, d.kind)
	}

	// Expect both node-yarn and node-monorepo.
	if !containsStr(kinds, "node-yarn") {
		t.Errorf("expected node-yarn in %v", kinds)
	}
	if !containsStr(kinds, "node-monorepo") {
		t.Errorf("expected node-monorepo in %v", kinds)
	}
}

func TestPackageJSONWorkspacesArray(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{
		"workspaces": ["packages/*", "apps/*"]
	}`)

	got := packageJSONWorkspaces(filepath.Join(dir, "package.json"))
	if len(got) != 2 {
		t.Fatalf("got %v, want [packages/* apps/*]", got)
	}
}

func TestPackageJSONWorkspacesObject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{
		"workspaces": { "packages": ["packages/*", "apps/*"] }
	}`)

	got := packageJSONWorkspaces(filepath.Join(dir, "package.json"))
	if len(got) != 2 {
		t.Fatalf("got %v, want [packages/* apps/*]", got)
	}
}

func TestPackageJSONWorkspacesNone(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name": "plain"}`)

	got := packageJSONWorkspaces(filepath.Join(dir, "package.json"))
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestPackageJSONWorkspacesMissing(t *testing.T) {
	got := packageJSONWorkspaces("/nonexistent/package.json")
	if len(got) != 0 {
		t.Fatalf("got %v, want empty for missing file", got)
	}
}

// TestInitDetectsBrokenSymlinkAsEnvExample pins M6: `has` uses Lstat,
// so an .env.example that is a broken symlink still counts as
// "present" for detection purposes. Under the old Stat-based check,
// this repo would silently miss the .env convention.
func TestInitDetectsBrokenSymlinkAsEnvExample(t *testing.T) {
	dir := t.TempDir()

	// Symlink to a non-existent target — Lstat succeeds, Stat does not.
	link := filepath.Join(dir, ".env.example")
	target := filepath.Join(dir, ".env.example.gone")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Sanity: the symlink target really is missing (so Stat would fail).
	if _, err := os.Stat(link); err == nil {
		t.Skip("environment resolves symlinks eagerly; test cannot exercise the Lstat path here")
	}

	got := detect(dir)

	found := false
	for _, d := range got {
		if d.kind == "env" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("detect(broken .env.example symlink) = %+v, want an 'env' detection", got)
	}
}

// TestInitExclusiveCreateDoesNotClobber pins M7: without --force, Init
// uses O_EXCL to open the target, so an existing file survives byte-
// for-byte and the error message tells the user how to override.
// Together these close the pre-M7 TOCTOU window where a racing writer
// could see the file wiped between the exists-check and the write.
func TestInitExclusiveCreateDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".wrk.yml")
	original := "resources:\n  - name: kept\n    path: kept\n"
	writeFile(t, target, original)

	// Also drop a detection so render() would produce non-empty content
	// if the O_EXCL guard failed.
	touch(t, filepath.Join(dir, "Gemfile"))

	err := Init(InitOptions{Root: dir, Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected error when .wrk.yml already exists without --force")
	}
	if !strings.Contains(err.Error(), "already exists") ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %q, want mention of 'already exists' and '--force'", err.Error())
	}

	// The exclusive-create path must not truncate: file content stays
	// exactly what we wrote.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != original {
		t.Fatalf("target was modified\n got: %q\nwant: %q", got, original)
	}
}


// --- helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
