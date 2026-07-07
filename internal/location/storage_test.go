package location

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/resolver"
)

func instance(
	relative string,
	fingerprintInputs ...string,
) resolver.ResourceInstance {
	return resolver.ResourceInstance{
		Resource: config.Resource{
			Name: "test",
			Path: relative,
		},
		Root: filepath.Clean("/repo"),
		WorkspacePath: filepath.Join(
			"/repo",
			relative,
		),
		RelativePath:      relative,
		FingerprintInputs: fingerprintInputs,
	}
}

func TestNonFingerprintedResource(t *testing.T) {
	location, err := For(
		"/storage",
		"github.com/acme/monolith",
		instance(".env"),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(
		"/storage",
		"github.com/acme/monolith",
		".env",
	)

	if location.Path != want {
		t.Fatalf(
			"got %q\nwant %q",
			location.Path,
			want,
		)
	}

	if location.Fingerprint != "" {
		t.Fatal("expected no fingerprint")
	}
}

func TestFingerprintedResource(t *testing.T) {
	root := t.TempDir()

	lock := filepath.Join(root, "yarn.lock")

	if err := os.WriteFile(
		lock,
		[]byte("hello"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	location, err := For(
		"/storage",
		"github.com/acme/monolith",
		instance(
			"node_modules",
			lock,
		),
	)
	if err != nil {
		t.Fatal(err)
	}

	if location.Fingerprint == "" {
		t.Fatal("expected fingerprint")
	}

	want := filepath.Join(
		"/storage",
		"github.com/acme/monolith",
		"node_modules",
		location.Fingerprint,
	)

	if location.Path != want {
		t.Fatalf(
			"got %q\nwant %q",
			location.Path,
			want,
		)
	}
}

func TestNestedResource(t *testing.T) {
	location, err := For(
		"/storage",
		"github.com/acme/monolith",
		instance("apps/web/node_modules"),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(
		"/storage",
		"github.com/acme/monolith",
		"apps",
		"web",
		"node_modules",
	)

	if location.Path != want {
		t.Fatalf(
			"got %q\nwant %q",
			location.Path,
			want,
		)
	}
}
