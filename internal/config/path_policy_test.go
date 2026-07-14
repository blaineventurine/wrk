package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDisallowedResourcePath pins the shared infrastructure/reserved-suffix
// policy that guards BOTH enforcement points: validate() for literal config
// paths and the resolver's glob-expansion filter. The three error classes
// are distinguished by message substring so a regression collapsing them
// (e.g. treating ".git/hooks" as an exact match, or a suffix check turning
// into a substring check) is caught, not just "some error".
func TestDisallowedResourcePath(t *testing.T) {
	cases := []struct {
		name string
		path string // slash-form; converted to the native separator below
		// errWant is the substring the error must contain; empty means
		// the path must be allowed (nil error).
		errWant string
	}{
		// Exact infrastructure paths.
		{"git dir", ".git", "would manage repository infrastructure"},
		{"jj dir", ".jj", "would manage repository infrastructure"},
		{"shared config file", ".wrk.yml", "would manage repository infrastructure"},
		{"local config file", ".wrk.local.yml", "would manage repository infrastructure"},

		// Nested under infrastructure (first path segment).
		{"nested under git", ".git/hooks", "inside repository infrastructure"},
		{"deeply nested under jj", ".jj/repo/store", "inside repository infrastructure"},

		// Legitimate paths: the infrastructure check is exact/first-segment,
		// never prefix or substring matching.
		{"plain resource", "node_modules", ""},
		{"git-prefixed top-level dir", ".gitx", ""},
		{"git-prefixed nested basename", "a/.gitx", ""},
		{"name containing git", "gitty", ""},
		{"nested plain resource", "packages/x", ""},

		// Executor-reserved basename suffixes.
		{"tmp suffix", "x.wrk-tmp", "reserved suffix"},
		{"lock suffix on nested basename", "dir/y.wrk-lock", "reserved suffix"},
		{"backup suffix", "z.wrk-backup", "reserved suffix"},

		// Suffix means SUFFIX: a basename merely containing a reserved
		// marker mid-string is a legitimate resource.
		{"reserved marker mid-basename", "x.wrk-tmp.d", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean := filepath.FromSlash(tc.path)
			err := DisallowedResourcePath(clean)

			if tc.errWant == "" {
				if err != nil {
					t.Fatalf(
						"DisallowedResourcePath(%q) = %v, want nil",
						clean, err,
					)
				}
				return
			}

			if err == nil {
				t.Fatalf(
					"DisallowedResourcePath(%q) = nil, want error containing %q",
					clean, tc.errWant,
				)
			}
			if !strings.Contains(err.Error(), tc.errWant) {
				t.Fatalf(
					"DisallowedResourcePath(%q) = %q, want to contain %q",
					clean, err.Error(), tc.errWant,
				)
			}
			// The message must name the offending path so users (via
			// validate) can locate the entry in a large config.
			if !strings.Contains(err.Error(), clean) {
				t.Fatalf(
					"DisallowedResourcePath(%q) = %q, want to name the path",
					clean, err.Error(),
				)
			}
		})
	}
}
