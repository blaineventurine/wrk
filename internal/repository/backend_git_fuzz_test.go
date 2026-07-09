package repository

import (
	"strings"
	"testing"
)

// FuzzParseWorktreePorcelain hammers parseWorktreePorcelain with random
// input to catch panics + logic bugs in git porcelain parsing.
// Invariants:
//
//   - Never panics on any input.
//   - Every returned path was extractable from some `worktree <value>`
//     line in the input, where TrimSpace(value) == path. The parser is
//     whitespace-tolerant (TrimSpace on the value) so this test mirrors
//     that tolerance rather than looking for the literal substring.
func FuzzParseWorktreePorcelain(f *testing.F) {
	seeds := []string{
		"",
		"worktree /path/main\nHEAD abcd\nbranch refs/heads/main\n\n",
		"worktree /path/main\nbare\n\n",
		"worktree /path/gone\nprunable gitdir file points to non-existent location\n\n",
		"worktree /path/a\nHEAD a\n\nworktree /path/b\nHEAD b\n\n",
		"worktree /has spaces/foo\nHEAD abcd\n\n",
		"worktree\n\n",
		"worktree /path\r\n\r\n",
		"garbage line\n\n",
		"worktree  0", // regression: whitespace-tolerant path extraction
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		paths := parseWorktreePorcelain(input)

		for _, p := range paths {
			if p == "" {
				t.Fatalf("empty path returned from input:\n%q", input)
			}
			if !hasWorktreeLineWithValue(input, p) {
				t.Fatalf("parser returned %q, but no worktree line in input has that value:\n%q",
					p, input)
			}
		}
	})
}

// FuzzParsePrunableWorktrees mirrors FuzzParseWorktreePorcelain for the
// prunable-side parser. Same panic invariant, plus: every returned
// ghost path lived in a record that also contained a prunable line and
// was NOT tagged bare.
func FuzzParsePrunableWorktrees(f *testing.F) {
	seeds := []string{
		"",
		"worktree /path/main\nHEAD abcd\nbranch refs/heads/main\n\n",
		"worktree /path/gone\nprunable gitdir file points to non-existent location\n\n",
		"worktree /path/main\nHEAD a\n\nworktree /path/gone\nprunable\n\n",
		"worktree /path/gone\nbare\nprunable\n\n",
		"prunable\n\n",
		"worktree  0",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		ghosts := parsePrunableWorktrees(input)

		if ghosts == nil {
			t.Fatal("parsePrunableWorktrees returned nil; contract requires empty slice")
		}

		for _, g := range ghosts {
			if g == "" {
				t.Fatalf("empty ghost path from input:\n%q", input)
			}
			if !hasPrunableRecordFor(input, g) {
				t.Fatalf("parser returned %q as prunable, but no matching record in input has both a worktree line and a prunable line without a bare line:\n%q",
					g, input)
			}
		}
	})
}

// hasWorktreeLineWithValue reports whether some line in input is a
// `worktree <value>` where TrimSpace(value) == path. Mirrors the
// parser's whitespace-tolerant extraction.
func hasWorktreeLineWithValue(input, path string) bool {
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimRight(line, "\r")
		key, value, hasValue := strings.Cut(line, " ")
		if key != "worktree" || !hasValue {
			continue
		}
		if strings.TrimSpace(value) == path {
			return true
		}
	}
	return false
}

// hasPrunableRecordFor reports whether some \n\n-separated record of
// input contains a `worktree <path>` line, a `prunable` line, and NOT
// a `bare` line. Mirrors parsePrunableWorktrees's selection logic.
func hasPrunableRecordFor(input, path string) bool {
	for _, record := range strings.Split(input, "\n\n") {
		var hasWorktree, hasPrunable, hasBare bool
		for _, line := range strings.Split(record, "\n") {
			line = strings.TrimRight(line, "\r")
			key, value, hasValue := strings.Cut(line, " ")
			switch key {
			case "worktree":
				if hasValue && strings.TrimSpace(value) == path {
					hasWorktree = true
				}
			case "bare":
				hasBare = true
			case "prunable":
				hasPrunable = true
			}
		}
		if hasWorktree && hasPrunable && !hasBare {
			return true
		}
	}
	return false
}
