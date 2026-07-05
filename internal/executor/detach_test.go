package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(
	t *testing.T,
	path string,
	contents string,
) {
	t.Helper()

	if err := os.MkdirAll(
		filepath.Dir(path),
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		path,
		[]byte(contents),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

func readFile(
	t *testing.T,
	path string,
) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestDetachFile(t *testing.T) {
	root := t.TempDir()

	shared := filepath.Join(root, "shared.env")
	writeFile(t, shared, "secret")

	link := filepath.Join(root, ".env")

	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	if err := detach(link, shared); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected regular file")
	}

	if got := readFile(t, link); got != "secret" {
		t.Fatalf(
			"got %q\nwant %q",
			got,
			"secret",
		)
	}

	// Modifying the detached copy must not affect the shared copy.
	writeFile(t, link, "local")

	if got := readFile(t, shared); got != "secret" {
		t.Fatalf(
			"shared resource changed: %q",
			got,
		)
	}
}

func TestDetachDirectory(t *testing.T) {
	root := t.TempDir()

	shared := filepath.Join(root, "shared")

	writeFile(
		t,
		filepath.Join(shared, "test.txt"),
		"hello",
	)

	link := filepath.Join(root, "node_modules")

	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	if err := detach(link, shared); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("expected directory")
	}

	if got := readFile(
		t,
		filepath.Join(link, "test.txt"),
	); got != "hello" {
		t.Fatalf(
			"got %q\nwant %q",
			got,
			"hello",
		)
	}

	// Modifying the detached copy must not affect the shared copy.
	writeFile(
		t,
		filepath.Join(link, "test.txt"),
		"local",
	)

	if got := readFile(
		t,
		filepath.Join(shared, "test.txt"),
	); got != "hello" {
		t.Fatalf(
			"shared resource changed: %q",
			got,
		)
	}
}

func TestDetachMissingTargetFails(t *testing.T) {
	root := t.TempDir()

	shared := filepath.Join(root, "missing")

	link := filepath.Join(root, "node_modules")

	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	if err := detach(link, shared); err == nil {
		t.Fatal("expected error")
	}
}

func TestDetachLeavesNoTemporaryFiles(t *testing.T) {
	root := t.TempDir()

	shared := filepath.Join(root, "shared")

	writeFile(
		t,
		filepath.Join(shared, "test.txt"),
		"hello",
	)

	link := filepath.Join(root, "node_modules")

	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	if err := detach(link, shared); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(link + ".wrk-tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary directory still exists")
	}

	if _, err := os.Stat(link + ".wrk-backup"); !os.IsNotExist(err) {
		t.Fatal("backup directory still exists")
	}
}
