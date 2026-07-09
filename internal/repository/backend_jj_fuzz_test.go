package repository

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzParseJJInlineError hammers parseJJInlineError with random name
// and template-output pairs. Invariants:
//
//   - Never panics on any input.
//   - When ok == true, the input starts with "<Error:" and ends with ">".
//   - When a path is returned (ok == true AND path != ""), the path is
//     filepath.Clean (idempotent under a second Clean).
func FuzzParseJJInlineError(f *testing.F) {
	seeds := []struct {
		name, s string
	}{
		{"", ""},
		{"feature", "feature\t/path/to/feature"},
		{"feature", "<Error: Failed to resolve workspace root: feature: /path/to/feature: No such file or directory>"},
		{"gone", "<Error: something entirely different>"},
		{"a", "<Error: Failed to resolve workspace root: a: /p: nope>"},
		{"name-with-dashes", "<Error: Failed to resolve workspace root: name-with-dashes: /x: y>"},
		{"", "<Error:>"},
		{"x", "<Error: Failed to resolve workspace root: no-colon-suffix>"},
	}
	for _, s := range seeds {
		f.Add(s.name, s.s)
	}

	f.Fuzz(func(t *testing.T, name, s string) {
		path, ok := parseJJInlineError(name, s)

		if ok {
			if !strings.HasPrefix(s, "<Error:") || !strings.HasSuffix(s, ">") {
				t.Fatalf("ok=true for input that does not start with '<Error:' and end with '>':\n%q",
					s)
			}
		}

		if path != "" {
			// filepath.Clean is idempotent; the parser is documented
			// to Clean before returning.
			if filepath.Clean(path) != path {
				t.Fatalf("returned path %q is not filepath.Clean-normalized (Clean → %q) from input:\n name=%q\n s=%q",
					path, filepath.Clean(path), name, s)
			}
		}
	})
}
