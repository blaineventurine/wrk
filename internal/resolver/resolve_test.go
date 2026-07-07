package resolver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

func touch(
	t *testing.T,
	path string,
) {
	t.Helper()

	if err := os.MkdirAll(
		filepath.Dir(path),
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = file.Close()
	}()
}

func TestLiteralPath(t *testing.T) {
	root := t.TempDir()

	resource := config.Resource{
		Name: "env",
		Path: ".env",
	}

	instances, err := Resolve(
		root,
		resource,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(instances) != 1 {
		t.Fatalf(
			"expected 1 instance, got %d",
			len(instances),
		)
	}

	instance := instances[0]

	if instance.Root != root {
		t.Fatalf(
			"unexpected root %q",
			instance.Root,
		)
	}

	if instance.RelativePath != ".env" {
		t.Fatalf(
			"unexpected relative path %q",
			instance.RelativePath,
		)
	}

	if len(instance.FingerprintInputs) != 0 {
		t.Fatal("expected no fingerprint inputs")
	}
}

func TestLiteralPathNeedNotExist(t *testing.T) {
	root := t.TempDir()

	resource := config.Resource{
		Name: "node",
		Path: "node_modules",
	}

	instances, err := Resolve(
		root,
		resource,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(instances) != 1 {
		t.Fatal("expected one instance")
	}
}

func TestGlobExpansion(t *testing.T) {
	root := t.TempDir()

	touch(
		t,
		filepath.Join(
			root,
			"apps",
			"web",
			"node_modules",
		),
	)

	touch(
		t,
		filepath.Join(
			root,
			"apps",
			"admin",
			"node_modules",
		),
	)

	resource := config.Resource{
		Name: "node",
		Path: "apps/*/node_modules",
	}

	instances, err := Resolve(
		root,
		resource,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(instances) != 2 {
		t.Fatalf(
			"expected 2 instances, got %d",
			len(instances),
		)
	}
}

func TestEmptyGlob(t *testing.T) {
	root := t.TempDir()

	resource := config.Resource{
		Name: "node",
		Path: "apps/*/node_modules",
	}

	instances, err := Resolve(
		root,
		resource,
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(instances) != 0 {
		t.Fatal("expected no instances")
	}
}

func TestParentPlaceholder(t *testing.T) {
	root := t.TempDir()

	touch(
		t,
		filepath.Join(
			root,
			"apps",
			"web",
			"node_modules",
		),
	)

	resource := config.Resource{
		Name: "node",
		Path: "apps/*/node_modules",

		Fingerprint: []string{
			"{parent}/package.json",
			"{root}/yarn.lock",
		},
	}

	instances, err := Resolve(
		root,
		resource,
	)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		filepath.Join(
			root,
			"apps",
			"web",
			"package.json",
		),
		filepath.Join(
			root,
			"yarn.lock",
		),
	}

	if !reflect.DeepEqual(
		instances[0].FingerprintInputs,
		want,
	) {
		t.Fatalf(
			"got %#v\nwant %#v",
			instances[0].FingerprintInputs,
			want,
		)
	}
}

func TestMatchPlaceholder(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(
		root,
		"apps",
		"web",
		"node_modules",
	)

	touch(t, path)

	resource := config.Resource{
		Name: "node",
		Path: "apps/*/node_modules",

		Fingerprint: []string{
			"{match}",
		},
	}

	instances, err := Resolve(
		root,
		resource,
	)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{path}

	if !reflect.DeepEqual(
		instances[0].FingerprintInputs,
		want,
	) {
		t.Fatalf(
			"got %#v\nwant %#v",
			instances[0].FingerprintInputs,
			want,
		)
	}
}
