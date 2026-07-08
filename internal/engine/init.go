package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/blaineventurine/wrk/examples"
)

// InitOptions controls how a starter .wrk.yml is generated.
type InitOptions struct {
	Root   string
	Force  bool
	DryRun bool
	Stdout io.Writer
}

type detection struct {
	kind string
}

// Init generates a starter .wrk.yml at options.Root by inspecting the
// directory for well-known project files.
func Init(options InitOptions) error {
	target := filepath.Join(options.Root, ".wrk.yml")

	if !options.DryRun && !options.Force {
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf(
				"%s already exists — use --force to overwrite",
				target,
			)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	detections := detect(options.Root)
	content := render(options.Root, detections)

	if options.DryRun {
		fmt.Fprintln(options.Stdout, "# Would write to:", target)
		fmt.Fprintln(options.Stdout)
		_, err := fmt.Fprint(options.Stdout, content)
		return err
	}

	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return err
	}

	fmt.Fprintf(options.Stdout, "✓ wrote %s\n", target)

	if len(detections) == 0 {
		fmt.Fprintln(options.Stdout,
			"  No project files detected — generated a commented template.\n"+
				"  Edit and run `wrk link` when ready.")
	} else {
		names := make([]string, 0, len(detections))
		for _, d := range detections {
			names = append(names, humanKind(d.kind))
		}
		fmt.Fprintf(options.Stdout, "  Detected: %s\n", strings.Join(names, ", "))
		fmt.Fprintln(options.Stdout, "  Next: run `wrk link` to provision.")
	}

	return nil
}

func detect(root string) []detection {
	var results []detection

	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}

	if has(".env.example") || has(".env.sample") {
		results = append(results, detection{"env"})
	}

	if has("package.json") {
		switch {
		case has("yarn.lock"):
			results = append(results, detection{"node-yarn"})
		case has("pnpm-lock.yaml"):
			results = append(results, detection{"node-pnpm"})
		case has("bun.lockb"):
			results = append(results, detection{"node-bun"})
		case has("package-lock.json"):
			results = append(results, detection{"node-npm"})
		default:
			results = append(results, detection{"node-nolock"})
		}
	}

	if has("Gemfile") {
		results = append(results, detection{"bundler"})
	}

	switch {
	case has("pyproject.toml") && has("uv.lock"):
		results = append(results, detection{"python-uv"})
	case has("pyproject.toml") && has("poetry.lock"):
		results = append(results, detection{"python-poetry"})
	case has("Pipfile.lock"):
		results = append(results, detection{"python-pipenv"})
	case has("requirements.txt"):
		results = append(results, detection{"python-pip"})
	}

	if has("Cargo.toml") {
		results = append(results, detection{"cargo-commented"})
	}

	// Monorepo: detect workspace layout from package.json "workspaces"
	// field and add a glob-based resource.
	if has("package.json") {
		if ws := packageJSONWorkspaces(filepath.Join(root, "package.json")); len(ws) > 0 {
			results = append(results, detection{"node-monorepo"})
		}
	}

	return results
}

// packageJSONWorkspaces reads the "workspaces" field from a package.json.
// Returns the raw patterns (e.g. ["packages/*", "apps/*"]) or nil.
func packageJSONWorkspaces(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// workspaces can be []string or {"packages": []string}
	var raw struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Workspaces == nil {
		return nil
	}

	var patterns []string
	if err := json.Unmarshal(raw.Workspaces, &patterns); err == nil {
		return patterns
	}

	var obj struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw.Workspaces, &obj); err == nil {
		return obj.Packages
	}

	return nil
}

func humanKind(kind string) string {
	switch kind {
	case "env":
		return ".env"
	case "node-yarn", "node-pnpm", "node-npm", "node-bun", "node-nolock":
		return "node_modules"
	case "node-monorepo":
		return "node_modules (workspaces)"
	case "bundler":
		return "vendor/bundle"
	case "python-uv", "python-poetry", "python-pipenv", "python-pip":
		return ".venv"
	case "cargo-commented":
		return "cargo target/ (commented)"
	}
	return kind
}

// render composes the generated .wrk.yml from YAML fragments embedded
// under examples/init/. When nothing is detected the entire file is a
// single commented walkthrough (empty.yml); otherwise a header block
// is followed by `resources:` and each detection's fragment indented
// two spaces to sit under it.
func render(root string, detections []detection) string {
	if len(detections) == 0 {
		return loadSnippet("empty.yml", nil)
	}

	var b strings.Builder
	b.WriteString(loadSnippet("header.yml", nil))
	b.WriteString("\nresources:\n")

	for _, d := range detections {
		b.WriteString("\n")
		b.WriteString(indent(snippetFor(root, d), "  "))
	}

	return b.String()
}

// snippetFor returns the raw YAML fragment for a detection kind. Only
// node-monorepo carries template data (the discovered workspace
// patterns); every other fragment is loaded verbatim.
func snippetFor(root string, d detection) string {
	name := d.kind + ".yml"

	if d.kind == "node-monorepo" {
		patterns := packageJSONWorkspaces(filepath.Join(root, "package.json"))
		return loadSnippet(name, map[string]string{
			"Patterns": strings.Join(patterns, ", "),
		})
	}

	return loadSnippet(name, nil)
}

// loadSnippet reads a fragment from the embedded examples/init/ FS,
// running it through text/template when data is non-nil. A missing
// file or template error is a programmer bug (the fragment set and
// the detection set are compiled in), so we panic instead of forcing
// every caller to plumb an unreachable error.
func loadSnippet(name string, data any) string {
	raw, err := examples.Init.ReadFile("init/" + name)
	if err != nil {
		panic(fmt.Sprintf("engine: missing init snippet %q: %v", name, err))
	}
	if data == nil {
		return string(raw)
	}

	tmpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		panic(fmt.Sprintf("engine: init snippet %q parse: %v", name, err))
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("engine: init snippet %q execute: %v", name, err))
	}
	return buf.String()
}

// indent prefixes every non-empty line of s with prefix, preserving
// blank lines so YAML comment blocks inside a fragment stay visually
// separated.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}
