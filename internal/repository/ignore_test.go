package repository

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func readExclude(t *testing.T, root string) string {
	t.Helper()

	data, err := os.ReadFile(
		filepath.Join(root, ".git", "info", "exclude"),
	)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
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

func TestPrepareCreatesExcludeFile(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env"); err != nil {
		t.Fatal(err)
	}

	exclude := filepath.Join(root, ".git", "info", "exclude")

	if _, err := os.Stat(exclude); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareAddsPatterns(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatal(err)
	}

	text := readExclude(t, root)

	if !strings.Contains(text, ".env") {
		t.Fatal("expected .env")
	}

	if !strings.Contains(text, "node_modules") {
		t.Fatal("expected node_modules")
	}
}

func TestPrepareIsIdempotent(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatal(err)
	}

	if err := repo.Prepare(".env", "node_modules"); err != nil {
		t.Fatal(err)
	}

	text := readExclude(t, root)

	if strings.Count(text, ".env") != 1 {
		t.Fatal("duplicate .env")
	}

	if strings.Count(text, "node_modules") != 1 {
		t.Fatal("duplicate node_modules")
	}
}

func TestPrepareFailsForDirectoryOnlyPattern(t *testing.T) {
	root := t.TempDir()

	exclude := filepath.Join(root, ".git", "info", "exclude")

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		exclude,
		[]byte("node_modules/\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	repo := newTestRepo(t, root)

	if err := repo.Prepare("node_modules"); err == nil {
		t.Fatal("expected error")
	}
}

// TestPrepareHeaderNotDuplicatedAcrossCalls verifies that consecutive
// Prepare calls that each add new patterns do not accrete duplicate
// "# Added by wrk" header lines.
func TestPrepareHeaderNotDuplicatedAcrossCalls(t *testing.T) {
	root := t.TempDir()
	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env"); err != nil {
		t.Fatal(err)
	}

	if err := repo.Prepare("node_modules"); err != nil {
		t.Fatal(err)
	}

	if err := repo.Prepare("dist"); err != nil {
		t.Fatal(err)
	}

	text := readExclude(t, root)

	if got := strings.Count(text, wrkHeader); got != 1 {
		t.Fatalf(
			"expected exactly one %q header, got %d\n---\n%s---",
			wrkHeader,
			got,
			text,
		)
	}

	for _, want := range []string{".env", "node_modules", "dist"} {
		if !strings.Contains(text, want+"\n") {
			t.Errorf("missing pattern %q in exclude", want)
		}
	}
}

// TestPrepareAtomicRewritePreservesUserContent verifies that the
// read-full/rewrite path preserves pre-existing user content
// (comments, patterns, blank lines) while appending new patterns.
func TestPrepareAtomicRewritePreservesUserContent(t *testing.T) {
	root := t.TempDir()

	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}

	original := "# my personal rules\n" +
		"*.log\n" +
		"\n" +
		"secret.txt\n"

	if err := os.WriteFile(
		exclude,
		[]byte(original),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	repo := newTestRepo(t, root)

	if err := repo.Prepare(".env"); err != nil {
		t.Fatal(err)
	}

	text := readExclude(t, root)

	if !strings.HasPrefix(text, original) {
		t.Fatalf("user content not preserved verbatim at head:\n---\n%s---", text)
	}

	if !strings.Contains(text, wrkHeader+"\n") {
		t.Errorf("missing wrk header")
	}

	if !strings.Contains(text, "\n.env\n") {
		t.Errorf("missing new pattern")
	}

	if strings.Count(text, wrkHeader) != 1 {
		t.Errorf("expected one header line, got %d", strings.Count(text, wrkHeader))
	}
}

// TestPrepareIsSafeForConcurrentInvocations spawns many goroutines,
// each adding a distinct pattern via ensureIgnored. The atomic
// tmp+rename write plus the in-process mutex must ensure every pattern
// survives and exactly one header line exists.
func TestPrepareIsSafeForConcurrentInvocations(t *testing.T) {
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

	text := readExclude(t, root)

	for i := range n {
		want := fmt.Sprintf("pattern-%02d", i)
		if !strings.Contains(text, want+"\n") {
			t.Errorf("missing %q\n---\n%s---", want, text)
		}
	}

	if got := strings.Count(text, wrkHeader); got != 1 {
		t.Errorf(
			"expected exactly one %q header, got %d\n---\n%s---",
			wrkHeader, got, text,
		)
	}
}

// TestPrepareDirectoryOnlyCollisionMessageNamesExcludeFile confirms
// the returned error tells the user which file to edit and which two
// patterns are in play.
func TestPrepareDirectoryOnlyCollisionMessageNamesExcludeFile(t *testing.T) {
	root := t.TempDir()

	exclude := filepath.Join(root, ".git", "info", "exclude")

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		exclude,
		[]byte("node_modules/\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	repo := newTestRepo(t, root)

	err := repo.Prepare("node_modules")
	if err == nil {
		t.Fatal("expected error")
	}

	msg := err.Error()

	if !strings.Contains(msg, exclude) {
		t.Errorf("error missing exclude path %q: %v", exclude, err)
	}

	if !strings.Contains(msg, "node_modules/") {
		t.Errorf("error missing existing pattern %q: %v", "node_modules/", err)
	}

	if !strings.Contains(msg, `"node_modules"`) {
		t.Errorf("error missing target pattern %q: %v", "node_modules", err)
	}
}

// TestPrepareDirectoryOnlyCollisionMatchesVariants covers the four
// normalized directory-only forms the collision guard recognises.
func TestPrepareDirectoryOnlyCollisionMatchesVariants(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"plain", "node_modules/\n"},
		{"leading-dot-slash", "./node_modules/\n"},
		{"leading-slash", "/node_modules/\n"},
		{"double-star", "**/node_modules/\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()

			exclude := filepath.Join(root, ".git", "info", "exclude")
			if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(
				exclude,
				[]byte(tc.content),
				0o644,
			); err != nil {
				t.Fatal(err)
			}

			repo := newTestRepo(t, root)

			err := repo.Prepare("node_modules")
			if err == nil {
				t.Fatalf("expected error for %s", tc.content)
			}

			// The existing (colliding) form should appear verbatim in
			// the message so the user can find the rule to edit.
			existing := strings.TrimSpace(tc.content)
			if !strings.Contains(err.Error(), existing) {
				t.Errorf(
					"expected error to mention existing form %q, got: %v",
					existing, err,
				)
			}
		})
	}
}

// TestReadPatternsWrapsScannerError feeds readPatterns a single line
// larger than the 1 MiB scanner buffer to force a scanner error, and
// verifies the returned error carries the file path so callers can
// tell which file failed.
func TestReadPatternsWrapsScannerError(t *testing.T) {
	// One line, 2 MiB, no newline: exceeds the scanner buffer max
	// and forces `bufio.ErrTooLong`.
	big := bytes.Repeat([]byte("a"), 2*1024*1024)

	path := "/some/repo/.git/info/exclude"
	_, err := readPatterns(bytes.NewReader(big), path)
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), path) {
		t.Errorf("error missing path %q: %v", path, err)
	}
}

// TestReadPatternsAcceptsLargeButValidRules ensures the enlarged
// buffer accepts long-but-legal rules (up to 1 MiB) without erroring.
func TestReadPatternsAcceptsLargeButValidRules(t *testing.T) {
	// A single 128 KiB line — larger than the default 64 KiB scanner
	// buffer but well under the 1 MiB max.
	long := bytes.Repeat([]byte("x"), 128*1024)
	data := append(long, '\n')

	patterns, err := readPatterns(bytes.NewReader(data), "unused")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !patterns[string(long)] {
		t.Fatal("expected long pattern to be parsed")
	}
}
