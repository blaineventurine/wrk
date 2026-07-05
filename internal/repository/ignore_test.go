package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readExclude(
	t *testing.T,
	root string,
) string {
	t.Helper()

	data, err := os.ReadFile(
		filepath.Join(
			root,
			".git",
			"info",
			"exclude",
		),
	)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestPrepareCreatesExcludeFile(t *testing.T) {
	root := t.TempDir()

	repo := newRepository(
		root,
		"local/test",
		filepath.Join(root, ".git"),
		Git,
	)

	if err := repo.Prepare(".env"); err != nil {
		t.Fatal(err)
	}

	exclude := filepath.Join(
		root,
		".git",
		"info",
		"exclude",
	)

	if _, err := os.Stat(exclude); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareAddsPatterns(t *testing.T) {
	root := t.TempDir()

	repo := newRepository(
		root,
		"local/test",
		filepath.Join(root, ".git"),
		Git,
	)

	if err := repo.Prepare(
		".env",
		"node_modules",
	); err != nil {
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

	repo := newRepository(
		root,
		"local/test",
		filepath.Join(root, ".git"),
		Git,
	)

	if err := repo.Prepare(
		".env",
		"node_modules",
	); err != nil {
		t.Fatal(err)
	}

	if err := repo.Prepare(
		".env",
		"node_modules",
	); err != nil {
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

	exclude := filepath.Join(
		root,
		".git",
		"info",
		"exclude",
	)

	if err := os.MkdirAll(
		filepath.Dir(exclude),
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		exclude,
		[]byte("node_modules/\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	repo := newRepository(
		root,
		"local/test",
		filepath.Join(root, ".git"),
		Git,
	)

	if err := repo.Prepare("node_modules"); err == nil {
		t.Fatal("expected error")
	}
}
