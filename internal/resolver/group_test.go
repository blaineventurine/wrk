package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

func TestResolveGroupMatchesAbsentLiteralLeaf(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"common/alpha", "common/zeta"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resource := config.Resource{
		Name:  "node",
		Paths: []string{"node_modules", "common/*/node_modules"},
	}

	instances, err := ResolveGroup(root, "", resource)
	if err != nil {
		t.Fatalf("ResolveGroup: %v", err)
	}
	want := []string{"common/alpha/node_modules", "common/zeta/node_modules", "node_modules"}
	if len(instances) != len(want) {
		t.Fatalf("instances = %v, want %v", relativePaths(instances), want)
	}
	for i := range want {
		if instances[i].RelativePath != want[i] {
			t.Fatalf("instances[%d] = %q, want %q", i, instances[i].RelativePath, want[i])
		}
	}
}

func relativePaths(instances []ResourceInstance) []string {
	result := make([]string, len(instances))
	for i := range instances {
		result[i] = instances[i].RelativePath
	}
	return result
}

func TestResolveGroupExpandsFingerprintGlobsOnceForTheGroup(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"packages/a/package.json", "packages/b/package.json"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	instances, err := ResolveGroup(root, "", config.Resource{
		Name:        "node",
		Paths:       []string{"node_modules"},
		Fingerprint: []string{"{root}/packages/*/package.json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := instances[0].FingerprintInputs
	if len(got) != 2 || filepath.Base(filepath.Dir(got[0])) != "a" || filepath.Base(filepath.Dir(got[1])) != "b" {
		t.Fatalf("fingerprint inputs = %v", got)
	}
}
