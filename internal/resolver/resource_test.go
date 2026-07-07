package resolver

import (
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

func TestResourceInstanceContext(t *testing.T) {
	root := filepath.FromSlash("/repo")
	workspacePath := filepath.Join(root, "vendor", "bundle")

	instance := ResourceInstance{
		Root:          root,
		WorkspacePath: workspacePath,
	}

	ctx := instance.Context("/shared/loc")

	if ctx.Root != root {
		t.Errorf("Root = %q, want %q", ctx.Root, root)
	}

	wantParent := filepath.Join(root, "vendor")
	if ctx.Parent != wantParent {
		t.Errorf("Parent = %q, want %q", ctx.Parent, wantParent)
	}

	if ctx.Match != workspacePath {
		t.Errorf("Match = %q, want %q", ctx.Match, workspacePath)
	}

	if ctx.Shared != "/shared/loc" {
		t.Errorf("Shared = %q, want %q", ctx.Shared, "/shared/loc")
	}
}

func TestNewInstanceFingerprintIgnoresShared(t *testing.T) {
	root := filepath.FromSlash("/repo")

	resource := config.Resource{
		Name: "bundler",
		Path: "vendor/bundle",
		Fingerprint: []string{
			"{root}/Gemfile",
			"{root}/Gemfile.lock",
			// {shared} must NOT be expanded here; a fingerprint that
			// depended on the shared path would be self-referential.
			"{shared}/ignored",
		},
	}

	instance, err := newInstance(
		root,
		resource,
		filepath.Join(root, "vendor", "bundle"),
	)
	if err != nil {
		t.Fatalf("newInstance returned error: %v", err)
	}

	want := []string{
		filepath.FromSlash("/repo/Gemfile"),
		filepath.FromSlash("/repo/Gemfile.lock"),
		// {shared} expands to "" -> "/ignored"
		filepath.FromSlash("/ignored"),
	}

	if len(instance.FingerprintInputs) != len(want) {
		t.Fatalf(
			"got %d fingerprint inputs, want %d: %v",
			len(instance.FingerprintInputs),
			len(want),
			instance.FingerprintInputs,
		)
	}

	for i, got := range instance.FingerprintInputs {
		if got != want[i] {
			t.Errorf(
				"FingerprintInputs[%d] = %q, want %q",
				i, got, want[i],
			)
		}
	}
}
