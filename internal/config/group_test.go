package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGroupedResource(t *testing.T) {
	root := t.TempDir()
	config := "resources:\n" +
		"  - name: yarn-node-modules\n" +
		"    paths:\n" +
		"      - node_modules\n" +
		"      - packages/*/node_modules\n"
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Resources[0].Grouped() {
		t.Fatal("resource with paths must be grouped")
	}
	if got := cfg.Resources[0].Paths; len(got) != 2 || got[1] != "packages/*/node_modules" {
		t.Fatalf("Paths = %v", got)
	}
}

func TestLoadRejectsInvalidGroupedResources(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"path and paths", "    path: node_modules\n    paths: [other]\n", "exactly one"},
		{"empty paths", "    paths: []\n", "paths must not be empty"},
		{"unsafe name", "    paths: [node_modules]\n", "group name"},
		{"duplicate paths", "    paths: [node_modules, node_modules]\n", "duplicate path"},
		{"escaping path", "    paths: [../node_modules]\n", "escapes"},
		{"instance fingerprint", "    paths: [node_modules]\n    fingerprint: [\"{parent}/package.json\"]\n", "only {root}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			name := "group"
			if tt.name == "unsafe name" {
				name = "group/name"
			}
			body := "resources:\n  - name: " + name + "\n" + tt.yaml
			if err := os.WriteFile(filepath.Join(root, Filename), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
