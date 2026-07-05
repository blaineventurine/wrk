package config

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
