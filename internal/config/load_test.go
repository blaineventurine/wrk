package config

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testdata/configs/*.yml
var exampleConfigs embed.FS

func copyFixture(
	t *testing.T,
	name string,
	root string,
) {
	t.Helper()

	data, err := exampleConfigs.ReadFile(
		"testdata/configs/" + name,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(
		filepath.Join(root, Filename),
		data,
		0o644,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	root := t.TempDir()

	_, err := Load(root)

	if err == nil {
		t.Fatal("expected an error")
	}

	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf(
			"expected ErrConfigNotFound, got %v",
			err,
		)
	}
}

func TestLoadDirectoryInsteadOfFile(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(root, Filename)

	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Load(root)

	if err == nil {
		t.Fatal("expected an error")
	}

	if !errors.Is(err, ErrConfigIsDirectory) {
		t.Fatalf(
			"expected ErrConfigIsDirectory, got %v",
			err,
		)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	root := t.TempDir()

	copyFixture(t, "empty.wrk.yml", root)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Resources) != 0 {
		t.Fatalf(
			"expected 0 resources, got %d",
			len(cfg.Resources),
		)
	}
}

func TestLoadSimpleResource(t *testing.T) {
	root := t.TempDir()

	copyFixture(t, "simple.wrk.yml", root)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	resource := cfg.Resources[0]

	if resource.Name != "env" {
		t.Fatalf("unexpected name: %q", resource.Name)
	}

	if resource.Path != ".env" {
		t.Fatalf("unexpected path: %q", resource.Path)
	}

	if len(resource.Fingerprint) != 0 {
		t.Fatal("expected empty fingerprint")
	}

	if len(resource.Hooks) != 0 {
		t.Fatal("expected empty hooks")
	}

	if !resource.ShouldCreate() {
		t.Fatal("expected ShouldCreate() to default to true")
	}
}

func TestLoadFingerprint(t *testing.T) {
	root := t.TempDir()

	copyFixture(t, "fingerprint.wrk.yml", root)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	resource := cfg.Resources[0]

	if len(resource.Fingerprint) != 2 {
		t.Fatal("expected two fingerprint entries")
	}
}

func TestLoadHooks(t *testing.T) {
	root := t.TempDir()

	copyFixture(t, "hooks.wrk.yml", root)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	commands := cfg.Resources[0].Hooks["initialize"]

	if len(commands) != 1 {
		t.Fatal("expected one initialize command")
	}

	if commands[0].Run != "bundle install" {
		t.Fatalf(
			"unexpected command: %q",
			commands[0].Run,
		)
	}
}

func TestLoadExplicitCreateFalse(t *testing.T) {
	root := t.TempDir()

	copyFixture(t, "create-false.wrk.yml", root)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Resources[0].ShouldCreate() {
		t.Fatal("expected ShouldCreate() to be false")
	}
}

func TestInvalidYAML(t *testing.T) {
	root := t.TempDir()

	copyFixture(t, "invalid.wrk.yml", root)

	_, err := Load(root)

	if err == nil {
		t.Fatal("expected an error")
	}

	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf(
			"expected ErrInvalidConfig, got %v",
			err,
		)
	}
}

func TestExampleConfigurations(t *testing.T) {
	err := fs.WalkDir(
		exampleConfigs,
		"testdata/configs",
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			switch filepath.Base(path) {
			case "invalid.wrk.yml":
				return nil
			}

			data, err := exampleConfigs.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			root := t.TempDir()

			err = os.WriteFile(
				filepath.Join(root, Filename),
				data,
				0o644,
			)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := Load(root); err != nil {
				t.Fatalf(
					"%s failed to load: %v",
					path,
					err,
				)
			}

			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWithoutLocalTagsShared(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Resources) != 1 {
		t.Fatalf("resources: got %d, want 1", len(cfg.Resources))
	}
	if got := cfg.Resources[0].Origin; got != OriginShared {
		t.Errorf("Origin = %q, want %q", got, OriginShared)
	}
}

func TestLoadLocalAdditions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: envrc
    path: .envrc
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Resources) != 2 {
		t.Fatalf("resources: got %d, want 2", len(cfg.Resources))
	}
	if cfg.Resources[0].Origin != OriginShared {
		t.Errorf("Resources[0].Origin = %q, want shared", cfg.Resources[0].Origin)
	}
	if cfg.Resources[1].Name != "envrc" ||
		cfg.Resources[1].Origin != OriginLocal {
		t.Errorf("Resources[1] = %+v, want name=envrc origin=local", cfg.Resources[1])
	}
}

func TestLoadLocalOverridesByName(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: node
    path: node_modules
    hooks:
      initialize:
        - run: yarn install
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: node
    path: node_modules
    hooks:
      initialize:
        - run: pnpm install
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Resources) != 1 {
		t.Fatalf("resources: got %d, want 1", len(cfg.Resources))
	}

	r := cfg.Resources[0]
	if r.Origin != OriginLocalOverride {
		t.Errorf("Origin = %q, want local-override", r.Origin)
	}
	if got := r.Hooks["initialize"][0].Run; got != "pnpm install" {
		t.Errorf("Hook.Run = %q, want pnpm install", got)
	}
}

func TestLoadLocalPreservesSharedOrder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: a
    path: a
  - name: b
    path: b
  - name: c
    path: c
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: b
    path: b-override
  - name: d
    path: d
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	got := []string{}
	for _, r := range cfg.Resources {
		got = append(got, r.Name)
	}
	want := []string{"a", "b", "c", "d"}

	if len(got) != len(want) {
		t.Fatalf("resource order: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Resources[%d].Name = %q, want %q", i, got[i], want[i])
		}
	}

	// Confirm b's origin is override and its path was taken from local.
	if cfg.Resources[1].Origin != OriginLocalOverride {
		t.Errorf("Resources[1].Origin = %q, want local-override", cfg.Resources[1].Origin)
	}
	if cfg.Resources[1].Path != "b-override" {
		t.Errorf("Resources[1].Path = %q, want b-override", cfg.Resources[1].Path)
	}
}

// TestValidateRejects covers every path/name validation rule via a table
// so adding new ones stays cheap.
func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		errWant string // substring the error must contain
	}{
		{
			name: "empty name",
			yaml: `
resources:
  - path: some_file
`,
			errWant: "name is required",
		},
		{
			name: "empty path",
			yaml: `
resources:
  - name: broken
`,
			errWant: "path is required",
		},
		{
			name: "duplicate names",
			yaml: `
resources:
  - name: env
    path: .env
  - name: env
    path: .env2
`,
			errWant: "duplicate name",
		},
		{
			name: "path is dot",
			yaml: `
resources:
  - name: root
    path: .
`,
			errWant: "repository root",
		},
		{
			name: "explicit empty path string",
			yaml: `
resources:
  - name: root
    path: ""
`,
			errWant: "path is required",
		},
		{
			name: "path is parent",
			yaml: `
resources:
  - name: escape
    path: ..
`,
			errWant: "escapes the repository root",
		},
		{
			name: "path with parent segment",
			yaml: `
resources:
  - name: escape
    path: ../foo
`,
			errWant: "escapes the repository root",
		},
		{
			name: "absolute path",
			yaml: `
resources:
  - name: abs
    path: /etc/something
`,
			errWant: "must be repository-relative",
		},
		{
			name: "path is .git",
			yaml: `
resources:
  - name: git
    path: .git
`,
			errWant: "repository infrastructure",
		},
		{
			name: "path is .jj",
			yaml: `
resources:
  - name: jj
    path: .jj
`,
			errWant: "repository infrastructure",
		},
		{
			name: "path is .wrk.yml",
			yaml: `
resources:
  - name: self
    path: .wrk.yml
`,
			errWant: "repository infrastructure",
		},
		{
			name: "path is .wrk.local.yml",
			yaml: `
resources:
  - name: self
    path: .wrk.local.yml
`,
			errWant: "repository infrastructure",
		},
		{
			name: "path inside .git",
			yaml: `
resources:
  - name: hooks
    path: .git/hooks
`,
			errWant: "inside repository infrastructure",
		},
		{
			name: "path inside .jj",
			yaml: `
resources:
  - name: store
    path: .jj/repo
`,
			errWant: "inside repository infrastructure",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, Filename), tc.yaml)

			_, err := Load(root)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errWant)
			}
			if !strings.Contains(err.Error(), tc.errWant) {
				t.Fatalf(
					"error = %q, want to contain %q",
					err.Error(),
					tc.errWant,
				)
			}
		})
	}
}

// TestValidateRejectsUserRegressionCase locks in the exact real-world
// misconfiguration that caused data loss in v0.1.0:
//
//	resources:
//	  - name: testFile
//	  - path: test-file.txt
//
// Two resources: one with only a name (empty path -> resolved to repo
// root), one with only a path (no name). Both must be rejected.
func TestValidateRejectsUserRegressionCase(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: testFile
  - path: test-file.txt
`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for the regression case, got nil")
	}
	// The load must reject on the first invalid field encountered — not
	// proceed silently past a resource with an empty path or empty name.
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf(
			"error = %q, expected it to complain about a required field",
			err.Error(),
		)
	}
}

// TestValidateAcceptsRealisticConfig confirms the happy path — a
// full-featured config exercising every optional field — is accepted.
func TestValidateAcceptsRealisticConfig(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
    create: false

  - name: node
    path: node_modules
    fingerprint:
      - "{root}/package.json"
      - "{root}/yarn.lock"
    hooks:
      initialize:
        - run: yarn install --frozen-lockfile
          cwd: "{root}"

  - name: bundler
    path: vendor/bundle
    fingerprint:
      - "{root}/Gemfile.lock"
    hooks:
      initialize:
        - run: bundle install
          cwd: "{root}"
          env:
            BUNDLE_PATH: "{shared}"
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("valid config was rejected: %v", err)
	}
	if len(cfg.Resources) != 3 {
		t.Fatalf("expected 3 resources, got %d", len(cfg.Resources))
	}
}

// TestValidateNestedPathsAllowed confirms we don't over-reject: a path
// like "config/env" is fine — the infrastructure guard only fires when
// the top-level segment is .git/.jj/.wrk.yml/.wrk.local.yml.
func TestValidateNestedPathsAllowed(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: nested
    path: config/env/values
`)

	if _, err := Load(root); err != nil {
		t.Fatalf("valid nested path rejected: %v", err)
	}
}

// TestValidateAppliesToLocalResources confirms the validation runs after
// the merge, so a local-only resource with an invalid path is caught.
func TestValidateAppliesToLocalResources(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: bad
    path: .git
`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected validation error from local-only resource, got nil")
	}
	if !strings.Contains(err.Error(), "repository infrastructure") {
		t.Fatalf("error = %q, expected infrastructure rejection", err.Error())
	}
}

// TestValidateAppliesToOverrides confirms an override can't sneak an
// invalid path past validation by targeting an existing shared name.
func TestValidateAppliesToOverrides(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: env
    path: ..
`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected validation error from override, got nil")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("error = %q, expected path-escape rejection", err.Error())
	}
}

func TestUserFacingExamplesAreValid(t *testing.T) {
	root, err := filepath.Abs("../../examples")
	if err != nil {
		t.Fatal(err)
	}

	entries, err := filepath.Glob(filepath.Join(root, "*", ".wrk.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no examples found under examples/")
	}

	for _, path := range entries {
		dir := filepath.Dir(path)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			if _, err := Load(dir); err != nil {
				t.Fatalf("%s failed to load: %v", dir, err)
			}
		})
	}
}

func TestLoadLocalOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: envrc
    path: .envrc
    create: false
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("expected local-only config to load, got %v", err)
	}
	if len(cfg.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(cfg.Resources))
	}
	if cfg.Resources[0].Origin != OriginLocal {
		t.Errorf("Origin = %q, want %q", cfg.Resources[0].Origin, OriginLocal)
	}
}

func TestLoadRejectsWhenNeitherExists(t *testing.T) {
	root := t.TempDir()

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error when neither .wrk.yml nor .wrk.local.yml exists")
	}
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

// TestMergeWarnsOnPathOverride pins that a local override with a
// different Path than the shared entry it replaces surfaces a
// human-readable warning through cfg.Warnings. A same-name override
// with a matching Path is silent.
func TestMergeWarnsOnPathOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
  - name: node
    path: node_modules
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: env
    path: env.dev
  - name: node
    path: node_modules
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one entry", cfg.Warnings)
	}

	w := cfg.Warnings[0]
	for _, want := range []string{`"env"`, `".env"`, `"env.dev"`, "redirects"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning %q missing %q", w, want)
		}
	}
}

// TestLoadNoWarningsWithoutOverride confirms Warnings stays nil for a
// vanilla shared-only load — nothing to complain about.
func TestLoadNoWarningsWithoutOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: env
    path: .env
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", cfg.Warnings)
	}
}

// TestLoadNoWarningsForMatchingPathOverride confirms that overriding
// the hook (but keeping the same Path) is silent — Warnings is reserved
// for redirections, not for every override.
func TestLoadNoWarningsForMatchingPathOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Filename), `
resources:
  - name: node
    path: node_modules
    hooks:
      initialize:
        - run: yarn install
`)
	writeFile(t, filepath.Join(root, LocalFilename), `
resources:
  - name: node
    path: node_modules
    hooks:
      initialize:
        - run: pnpm install
`)

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none (path unchanged)", cfg.Warnings)
	}
}
