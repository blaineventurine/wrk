package repository

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

func excludePath(root string) string {
	return filepath.Join(root, ".git", "info", "exclude")
}

func readExclude(t *testing.T, root string) string {
	t.Helper()

	data, err := os.ReadFile(excludePath(root))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func writeExclude(t *testing.T, root, content string) {
	t.Helper()

	exclude := excludePath(root)
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestRepo(t *testing.T, root string) *Repository {
	t.Helper()

	return newRepository(
		root,
		"local/test",
		filepath.Join(root, ".git"),
		gitBackend{},
	)
}

// splitExcludeLines splits file content into lines the way the tests
// reason about them: no trailing empty element for the final newline,
// nil for an empty file.
func splitExcludeLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

// splitBlock partitions lines into (outside-the-block, block-interior)
// using the header/footer markers. hasBlock is false when no header
// line exists; a header without a footer fails the calling test via
// the marker-count assertions instead.
func splitBlock(t testingFataler, lines []string) (outside, block []string, hasBlock bool) {
	h := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == wrkHeader {
			h = i
			break
		}
	}
	if h < 0 {
		return lines, nil, false
	}
	for j := h + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == wrkFooter {
			outside = append(outside, lines[:h]...)
			outside = append(outside, lines[j+1:]...)
			return outside, lines[h+1 : j], true
		}
	}
	t.Fatalf("header present without footer in %q", strings.Join(lines, "\n"))
	return nil, nil, false
}

// testingFataler lets splitBlock serve both *testing.T and *rapid.T.
type testingFataler interface {
	Fatalf(format string, args ...any)
}

func nonBlankLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func countMarker(lines []string, marker string) int {
	n := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == marker {
			n++
		}
	}
	return n
}

// captureStderr runs fn with os.Stderr swapped for a pipe and returns
// everything fn wrote to it. Not safe for parallel tests; none of the
// callers use t.Parallel().
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(data)
}

// TestPrepareCreatesBlockWithHeaderAndFooter pins the modern block
// shape on a fresh repository: header, the exact patterns in input
// order, footer, one trailing newline — nothing else.
func TestPrepareCreatesBlockWithHeaderAndFooter(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatal(err)
	}

	want := wrkHeader + "\n.env\nnode_modules\n" + wrkFooter + "\n"
	if got := readExclude(t, root); got != want {
		t.Fatalf("exclude content:\ngot  %q\nwant %q", got, want)
	}
}

// TestPrepareDeduplicatesInInputOrder pins that repeated paths collapse
// to their first occurrence and the block preserves input order (it is
// NOT sorted).
func TestPrepareDeduplicatesInInputOrder(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare("b/x", "a", "b/x", "a"); err != nil {
		t.Fatal(err)
	}

	want := wrkHeader + "\nb/x\na\n" + wrkFooter + "\n"
	if got := readExclude(t, root); got != want {
		t.Fatalf("exclude content:\ngot  %q\nwant %q", got, want)
	}
}

// TestPreparePrunesRemovedPatterns pins the rebuild-from-config
// contract: a pattern absent from the next Prepare call disappears
// from the block instead of accreting forever.
func TestPreparePrunesRemovedPatterns(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Prepare(".env"); err != nil {
		t.Fatal(err)
	}

	got := readExclude(t, root)
	want := wrkHeader + "\n.env\n" + wrkFooter + "\n"
	if got != want {
		t.Fatalf("exclude content:\ngot  %q\nwant %q", got, want)
	}
	if strings.Contains(got, "node_modules") {
		t.Fatalf("removed pattern survived: %q", got)
	}
}

// TestPrepareEmptyManagedSetOmitsBlock pins that a Prepare with no
// needed patterns removes the whole block (markers included) while
// leaving user content alone.
func TestPrepareEmptyManagedSetOmitsBlock(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)
	writeExclude(t, root, "keep\n")

	if err := repo.Prepare("x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Prepare(); err != nil {
		t.Fatal(err)
	}

	got := readExclude(t, root)
	if got != "keep\n" {
		t.Fatalf("exclude content:\ngot  %q\nwant %q", got, "keep\n")
	}
}

// TestPreparePreservesUserPrefixAndSuffix pins byte-for-byte
// preservation of user content around a modern block while the block
// interior is rewritten.
func TestPreparePreservesUserPrefixAndSuffix(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	prefix := "# personal\n*.log\n\n"
	// The suffix starts with a blank separator line — the shape wrk
	// itself writes after the footer — so preservation is exact.
	suffix := "\n# tail comment\nsecret.txt\n"
	writeExclude(t, root,
		prefix+wrkHeader+"\nold1\nold2\n"+wrkFooter+"\n"+suffix)

	if err := repo.Prepare("new"); err != nil {
		t.Fatal(err)
	}

	got := readExclude(t, root)
	want := prefix + wrkHeader + "\nnew\n" + wrkFooter + "\n" + suffix
	if got != want {
		t.Fatalf("exclude content:\ngot  %q\nwant %q", got, want)
	}
}

// TestPrepareDoesNotDuplicateUserRule pins that a pattern the user
// already ignores with their own rule stays out of the managed block.
func TestPrepareDoesNotDuplicateUserRule(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)
	writeExclude(t, root, "node_modules\n")

	if err := repo.Prepare("node_modules", ".env"); err != nil {
		t.Fatal(err)
	}

	got := readExclude(t, root)
	want := "node_modules\n\n" + wrkHeader + "\n.env\n" + wrkFooter + "\n"
	if got != want {
		t.Fatalf("exclude content:\ngot  %q\nwant %q", got, want)
	}
	if strings.Count(got, "node_modules") != 1 {
		t.Fatalf("user rule duplicated: %q", got)
	}
}

// TestPrepareLegacyHeaderUpgrade pins the upgrade path for files
// written by pre-footer wrk versions: the contiguous run of non-blank,
// non-comment lines after the header is adopted (and rewritten); the
// first blank or comment line ends the legacy block and everything
// from there on is user content.
func TestPrepareLegacyHeaderUpgrade(t *testing.T) {
	cases := []struct {
		name     string
		original string
		want     string
	}{
		{
			name: "blank line ends legacy block",
			original: "user-top\n" + wrkHeader +
				"\nlegacy1\nlegacy2\n\nuser-tail\n",
			want: "user-top\n\n" + wrkHeader + "\nfresh\n" + wrkFooter +
				"\n\nuser-tail\n",
		},
		{
			name: "comment line ends legacy block",
			original: wrkHeader +
				"\nlegacy1\n# user comment\nuser-rule\n",
			want: wrkHeader + "\nfresh\n" + wrkFooter +
				"\n\n# user comment\nuser-rule\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repo := newTestRepo(t, root)
			writeExclude(t, root, tc.original)

			if err := repo.Prepare("fresh"); err != nil {
				t.Fatal(err)
			}

			got := readExclude(t, root)
			if got != tc.want {
				t.Fatalf("exclude content:\ngot  %q\nwant %q", got, tc.want)
			}
			if strings.Contains(got, "legacy1") || strings.Contains(got, "legacy2") {
				t.Fatalf("legacy block content survived the upgrade: %q", got)
			}
		})
	}
}

// TestPrepareDirectoryOnlyCollisionWarnsAndAdds pins the softened
// collision behavior: a user directory-only rule no longer blocks
// Prepare. The slash-less pattern is added anyway (that is what covers
// wrk's symlink) and a warning lands on stderr.
func TestPrepareDirectoryOnlyCollisionWarnsAndAdds(t *testing.T) {
	forms := []struct {
		name string
		rule string
	}{
		{"plain", "node_modules/"},
		{"leading-dot-slash", "./node_modules/"},
		{"leading-slash", "/node_modules/"},
		{"double-star", "**/node_modules/"},
	}

	for _, tc := range forms {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repo := newTestRepo(t, root)
			writeExclude(t, root, tc.rule+"\n")

			var prepErr error
			stderr := captureStderr(t, func() {
				prepErr = repo.Prepare("node_modules")
			})
			if prepErr != nil {
				t.Fatalf("Prepare with %q collision: %v", tc.rule, prepErr)
			}

			// Load-bearing: the slash-less pattern made it into the
			// block and the user's rule survived.
			got := readExclude(t, root)
			want := tc.rule + "\n\n" + wrkHeader + "\nnode_modules\n" + wrkFooter + "\n"
			if got != want {
				t.Fatalf("exclude content:\ngot  %q\nwant %q", got, want)
			}

			if !strings.Contains(stderr, "directory-only") {
				t.Errorf("stderr warning missing %q: %q", "directory-only", stderr)
			}
			if !strings.Contains(stderr, tc.rule) {
				t.Errorf("stderr warning missing colliding rule %q: %q", tc.rule, stderr)
			}
		})
	}
}

// TestPrepareSecondCallIsByteStableWithoutRewrite pins idempotency:
// re-running Prepare with the same inputs leaves the file byte-
// identical AND skips the write entirely. The info directory is made
// read-only for the second call, so any attempted rewrite (which goes
// through a temp file in that directory) would surface as an error.
func TestPrepareSecondCallIsByteStableWithoutRewrite(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)
	writeExclude(t, root, "mine\n")

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatal(err)
	}
	first := readExclude(t, root)

	infoDir := filepath.Dir(excludePath(root))
	if err := os.Chmod(infoDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(infoDir, 0o755) })

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatalf("second Prepare attempted a rewrite: %v", err)
	}

	if second := readExclude(t, root); second != first {
		t.Fatalf("second Prepare not byte-stable:\nfirst  %q\nsecond %q", first, second)
	}
}

// TestPrepareNormalizesTrailingWhitespace pins the two documented
// deviations from byte-for-byte preservation: EOF-newline
// normalization and trimming of a trailing blank-line run.
func TestPrepareNormalizesTrailingWhitespace(t *testing.T) {
	cases := []struct {
		name     string
		original string
		paths    []string
		want     string
	}{
		{
			name:     "trailing blank lines trimmed",
			original: "a\n\n\n",
			want:     "a\n",
		},
		{
			name:     "trailing whitespace-only line trimmed",
			original: "a\n   \n",
			want:     "a\n",
		},
		{
			name:     "missing EOF newline normalized",
			original: "a",
			paths:    []string{"p"},
			want:     "a\n\n" + wrkHeader + "\np\n" + wrkFooter + "\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repo := newTestRepo(t, root)
			writeExclude(t, root, tc.original)

			if err := repo.Prepare(tc.paths...); err != nil {
				t.Fatal(err)
			}

			if got := readExclude(t, root); got != tc.want {
				t.Fatalf("exclude content:\ngot  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestPrepareConcurrentCallsKeepFileWellFormed spawns goroutines each
// rewriting the block with a distinct single pattern. The in-process
// mutex plus tmp+rename write must keep the file well-formed under the
// race: exactly one header/footer pair and a block holding exactly one
// of the contended patterns (each call REPLACES the block, so the last
// writer wins — surviving all patterns would itself be a bug).
func TestPrepareConcurrentCallsKeepFileWellFormed(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pattern := fmt.Sprintf("pattern-%02d", i)
			if err := repo.ensureIgnored([]string{pattern}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent ensureIgnored: %v", err)
	}

	lines := splitExcludeLines(readExclude(t, root))
	if got := countMarker(lines, wrkHeader); got != 1 {
		t.Fatalf("header count = %d, want 1\n---\n%s", got, strings.Join(lines, "\n"))
	}
	if got := countMarker(lines, wrkFooter); got != 1 {
		t.Fatalf("footer count = %d, want 1\n---\n%s", got, strings.Join(lines, "\n"))
	}

	_, block, hasBlock := splitBlock(t, lines)
	if !hasBlock || len(block) != 1 {
		t.Fatalf("block = %v, want exactly one pattern", block)
	}
	if !strings.HasPrefix(block[0], "pattern-") || len(block[0]) != len("pattern-00") {
		t.Fatalf("block content %q is not one of the contended patterns", block[0])
	}
}

// propManagedPool is the universe of managed patterns the property
// test draws from; user pattern lines draw from the SAME pool so the
// outside-rule dedup path is exercised.
var propManagedPool = []string{"alpha", "beta", "gamma", ".env", "node_modules", "pkg/sub"}

// propDirOnlyPool feeds user directory-only rules. Deliberately
// disjoint from propManagedPool so the property run never trips the
// collision warning (that path has its own focused test).
var propDirOnlyPool = []string{"cachex", "tmpout"}

// TestPrepareRewriteProperty drives Prepare over random marker-free
// user exclude files and random desired sets, asserting:
//
//	(a) every non-blank user line survives verbatim, in order;
//	(b) every desired pattern is present as a line;
//	(c) a second identical Prepare is byte-stable;
//	(d) header and footer each appear at most once, always paired;
//	(e) the block interior is EXACTLY the input-order dedup of the
//	    desired set minus patterns the user already rules outside.
func TestPrepareRewriteProperty(t *testing.T) {
	userLineGen := rapid.Custom(func(rt *rapid.T) string {
		switch rapid.IntRange(0, 4).Draw(rt, "kind") {
		case 0:
			return ""
		case 1:
			return "# " + rapid.StringMatching(`[a-z]{1,8}`).Draw(rt, "comment")
		case 2:
			return rapid.SampledFrom(propManagedPool).Draw(rt, "pattern")
		case 3:
			return "  " + rapid.SampledFrom(propManagedPool).Draw(rt, "padded")
		default:
			return rapid.SampledFrom(propDirOnlyPool).Draw(rt, "dironly") + "/"
		}
	})

	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		repo := newTestRepo(t, root)

		userLines := rapid.SliceOfN(userLineGen, 0, 10).Draw(rt, "userLines")
		desired := rapid.SliceOfN(
			rapid.SampledFrom(propManagedPool), 1, 5,
		).Draw(rt, "desired")

		if len(userLines) > 0 {
			data := strings.Join(userLines, "\n")
			if rapid.Bool().Draw(rt, "trailingNewline") {
				data += "\n"
			}
			writeExclude(t, root, data)
		}

		if err := repo.Prepare(desired...); err != nil {
			rt.Fatalf("Prepare: %v", err)
		}

		first, err := os.ReadFile(excludePath(root))
		if err != nil {
			rt.Fatalf("read exclude: %v", err)
		}
		lines := splitExcludeLines(string(first))

		// (d) markers at most once, and always paired.
		headers := countMarker(lines, wrkHeader)
		footers := countMarker(lines, wrkFooter)
		if headers > 1 || footers > 1 || headers != footers {
			rt.Fatalf("marker counts header=%d footer=%d in %q", headers, footers, first)
		}

		outsideLines, block, hasBlock := splitBlock(rt, lines)

		// (a) non-blank user lines survive verbatim, in order.
		if got, want := nonBlankLines(outsideLines), nonBlankLines(userLines); !reflect.DeepEqual(got, want) {
			rt.Fatalf("user lines mangled:\ngot  %q\nwant %q\nfile %q", got, want, first)
		}

		// (b) every desired pattern is present as a line.
		for _, p := range desired {
			want := filepath.ToSlash(p)
			found := false
			for _, line := range lines {
				if strings.TrimSpace(line) == want {
					found = true
					break
				}
			}
			if !found {
				rt.Fatalf("desired pattern %q missing from %q", want, first)
			}
		}

		// (e) block interior is exactly the needed set.
		outside := make(map[string]bool)
		for _, line := range userLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			outside[trimmed] = true
		}
		seen := make(map[string]bool)
		var wantBlock []string
		for _, p := range desired {
			q := filepath.ToSlash(p)
			if seen[q] || outside[q] {
				continue
			}
			seen[q] = true
			wantBlock = append(wantBlock, q)
		}
		if len(wantBlock) == 0 {
			if hasBlock {
				rt.Fatalf("expected no block, got %q in %q", block, first)
			}
		} else {
			if !hasBlock {
				rt.Fatalf("expected block %q, found none in %q", wantBlock, first)
			}
			if !reflect.DeepEqual(block, wantBlock) {
				rt.Fatalf("block:\ngot  %q\nwant %q", block, wantBlock)
			}
		}

		// (c) second identical Prepare is byte-stable.
		if err := repo.Prepare(desired...); err != nil {
			rt.Fatalf("second Prepare: %v", err)
		}
		second, err := os.ReadFile(excludePath(root))
		if err != nil {
			rt.Fatalf("re-read exclude: %v", err)
		}
		if !bytes.Equal(first, second) {
			rt.Fatalf("second Prepare not byte-stable:\nfirst  %q\nsecond %q", first, second)
		}
	})
}

// FuzzPrepareExcludeRewrite hammers Prepare with arbitrary pre-existing
// exclude bytes and an arbitrary single pattern. Invariants:
//
//   - never panics, never errors (Prepare does no path I/O on the
//     pattern itself);
//   - output is newline-terminated with no trailing blank line;
//   - a sane single-line pattern is always present afterwards (either
//     in the block or as a pre-existing user rule);
//   - a second identical Prepare is byte-stable.
//
// Patterns that trim-equal wrk's own marker strings are out of
// contract (resource paths never look like the markers) and skipped.
func FuzzPrepareExcludeRewrite(f *testing.F) {
	seeds := []struct{ user, pattern string }{
		{"", ".env"},
		{"node_modules/\n", "node_modules"},
		{"# personal\n*.log\n\nsecret.txt\n", ".env"},
		{wrkHeader + "\nlegacy\n", "fresh"},
		{wrkHeader + "\na\n" + wrkFooter + "\nuser\n", "a"},
		{wrkFooter + "\n" + wrkHeader + "\n", "x"},
		{"a\n\n\n", "b"},
		{"a", "a"},
		{"\xff\xfe\x00binary\n", "p"},
		{"", "  padded  "},
		{"", "# comment-looking"},
		{"", "multi\nline"},
	}
	for _, s := range seeds {
		f.Add(s.user, s.pattern)
	}

	f.Fuzz(func(t *testing.T, userFile string, pattern string) {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == wrkHeader || trimmed == wrkFooter {
			t.Skip("marker strings are not resource paths")
		}

		root := t.TempDir()
		repo := newTestRepo(t, root)
		writeExclude(t, root, userFile)

		if err := repo.Prepare(pattern); err != nil {
			t.Fatalf("Prepare(%q) on %q: %v", pattern, userFile, err)
		}

		first, err := os.ReadFile(excludePath(root))
		if err != nil {
			t.Fatal(err)
		}

		if len(first) > 0 {
			text := string(first)
			if !strings.HasSuffix(text, "\n") {
				t.Fatalf("output missing EOF newline: %q", text)
			}
			lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
			if strings.TrimSpace(lines[len(lines)-1]) == "" {
				t.Fatalf("trailing blank line survived: %q", text)
			}
			if trimmed != "" && !strings.ContainsAny(pattern, "\r\n") {
				found := false
				for _, line := range lines {
					if strings.TrimSpace(line) == trimmed {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("pattern %q missing after Prepare: %q", pattern, text)
				}
			}
		}

		if err := repo.Prepare(pattern); err != nil {
			t.Fatalf("second Prepare(%q): %v", pattern, err)
		}
		second, err := os.ReadFile(excludePath(root))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("second Prepare not byte-stable:\nfirst  %q\nsecond %q", first, second)
		}
	})
}
