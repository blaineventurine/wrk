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

	// {root} is the only placeholder that gives a sensible fingerprint
	// input rooted in the repo; {shared} would expand to "" (yielding a
	// path outside the repo) — that case is covered by
	// TestNewInstanceFingerprintRejectsSharedPlaceholder.
	resource := config.Resource{
		Name: "bundler",
		Path: "vendor/bundle",
		Fingerprint: []string{
			"{root}/Gemfile",
			"{root}/Gemfile.lock",
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

// TestNewInstanceFingerprintRejectsSharedPlaceholder pins the contract
// that {shared} in a fingerprint input is rejected. Fingerprints are
// expanded with an empty shared path (so they never depend on the shared
// location), which means {shared}/whatever collapses to /whatever — a
// path outside the repo. The containment check catches that.
func TestNewInstanceFingerprintRejectsSharedPlaceholder(t *testing.T) {
	root := filepath.FromSlash("/repo")

	resource := config.Resource{
		Name: "bundler",
		Path: "vendor/bundle",
		Fingerprint: []string{
			"{shared}/ignored",
		},
	}

	_, err := newInstance(
		root,
		resource,
		filepath.Join(root, "vendor", "bundle"),
	)
	if err == nil {
		t.Fatal("expected error for {shared}-rooted fingerprint input")
	}
}

// TestNewInstanceFingerprintRejectsEscape pins the containment check
// itself: a fingerprint input that resolves outside the repository root
// via `..` MUST be rejected up front.
func TestNewInstanceFingerprintRejectsEscape(t *testing.T) {
	root := filepath.FromSlash("/repo")

	resource := config.Resource{
		Name: "bundler",
		Path: "vendor/bundle",
		Fingerprint: []string{
			"{root}/../secret",
		},
	}

	_, err := newInstance(
		root,
		resource,
		filepath.Join(root, "vendor", "bundle"),
	)
	if err == nil {
		t.Fatal("expected error for fingerprint input escaping repo root")
	}
}

// TestNewInstanceFingerprintRejectsUnknownPlaceholder pins the strict-
// expand upgrade: a typo like {shred} MUST error rather than passing
// through as a literal path component.
func TestNewInstanceFingerprintRejectsUnknownPlaceholder(t *testing.T) {
	root := filepath.FromSlash("/repo")

	resource := config.Resource{
		Name: "bundler",
		Path: "vendor/bundle",
		Fingerprint: []string{
			"{shred}/Gemfile",
		},
	}

	_, err := newInstance(
		root,
		resource,
		filepath.Join(root, "vendor", "bundle"),
	)
	if err == nil {
		t.Fatal("expected error for unknown placeholder in fingerprint")
	}
}
