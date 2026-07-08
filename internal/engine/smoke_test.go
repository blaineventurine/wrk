package engine_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

// TestInitSnippetsRoundTripThroughConfigLoad exercises every detection
// kind end-to-end: run Init on a synthetic project, then load the
// generated .wrk.yml back through config.Load. Guards against snippet
// files drifting into invalid YAML (the pre-refactor monorepo case
// leaked raw tabs and would have failed here).
func TestInitSnippetsRoundTripThroughConfigLoad(t *testing.T) {
	scenarios := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"env", map[string]string{".env.example": ""}, []string{"env"}},
		{"node-yarn", map[string]string{"package.json": "{}", "yarn.lock": ""}, []string{"node"}},
		{"node-pnpm", map[string]string{"package.json": "{}", "pnpm-lock.yaml": ""}, []string{"node"}},
		{"node-bun", map[string]string{"package.json": "{}", "bun.lockb": ""}, []string{"node"}},
		{"node-npm", map[string]string{"package.json": "{}", "package-lock.json": ""}, []string{"node"}},
		{"node-nolock", map[string]string{"package.json": "{}"}, []string{"node"}},
		{"bundler", map[string]string{"Gemfile": ""}, []string{"bundler"}},
		{"python-uv", map[string]string{"pyproject.toml": "", "uv.lock": ""}, []string{"python"}},
		{"python-poetry", map[string]string{"pyproject.toml": "", "poetry.lock": ""}, []string{"python"}},
		{"python-pipenv", map[string]string{"Pipfile.lock": ""}, []string{"python"}},
		{"python-pip", map[string]string{"requirements.txt": ""}, []string{"python"}},
		{"cargo", map[string]string{"Cargo.toml": ""}, nil}, // commented-out
		{"monorepo", map[string]string{
			"package.json": `{"name":"m","workspaces":["packages/*","apps/*"]}`,
			"yarn.lock":    "",
		}, []string{"node", "node-workspaces"}},
	}

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range s.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			if err := engine.Init(engine.InitOptions{Root: dir, Stdout: &out}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			cfg, err := config.Load(dir)
			if err != nil {
				body, _ := os.ReadFile(filepath.Join(dir, ".wrk.yml"))
				t.Fatalf("config.Load: %v\ngenerated:\n%s", err, body)
			}
			got := make([]string, 0, len(cfg.Resources))
			for _, r := range cfg.Resources {
				got = append(got, r.Name)
			}
			if !equalStrings(got, s.want) {
				t.Errorf("resources = %v, want %v", got, s.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
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
