package fingerprint

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

func fingerprintOf(
	t *testing.T,
	root string,
	paths ...string,
) string {
	t.Helper()

	fp, err := Fingerprint(root, paths...)
	if err != nil {
		t.Fatal(err)
	}

	return fp
}

func TestSameFileSameFingerprint(t *testing.T) {
	root := t.TempDir()

	file := filepath.Join(root, "foo.txt")

	writeFile(t, file, "hello")

	first := fingerprintOf(t, root, file)
	second := fingerprintOf(t, root, file)

	if first != second {
		t.Fatalf(
			"expected identical fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}

func TestContentsChangeChangesFingerprint(t *testing.T) {
	root := t.TempDir()

	file := filepath.Join(root, "foo.txt")

	writeFile(t, file, "hello")

	before := fingerprintOf(t, root, file)

	writeFile(t, file, "goodbye")

	after := fingerprintOf(t, root, file)

	if before == after {
		t.Fatalf(
			"expected fingerprint to change\nbefore: %s\nafter:  %s",
			before,
			after,
		)
	}
}

func TestFilenameChangesFingerprint(t *testing.T) {
	root := t.TempDir()

	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")

	writeFile(t, a, "hello")
	writeFile(t, b, "hello")

	first := fingerprintOf(t, root, a)
	second := fingerprintOf(t, root, b)

	if first == second {
		t.Fatalf(
			"expected different fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}

func TestMissingFileDeterministic(t *testing.T) {
	root := t.TempDir()

	missing := filepath.Join(root, "missing.txt")

	first := fingerprintOf(t, root, missing)
	second := fingerprintOf(t, root, missing)

	if first != second {
		t.Fatalf(
			"expected identical fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}

func TestMissingVsExisting(t *testing.T) {
	root := t.TempDir()

	file := filepath.Join(root, "missing.txt")

	before := fingerprintOf(t, root, file)

	writeFile(t, file, "hello")

	after := fingerprintOf(t, root, file)

	if before == after {
		t.Fatalf(
			"expected fingerprint to change\nbefore: %s\nafter:  %s",
			before,
			after,
		)
	}
}

func TestOrderDoesNotMatter(t *testing.T) {
	root := t.TempDir()

	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	c := filepath.Join(root, "c.txt")

	writeFile(t, a, "A")
	writeFile(t, b, "B")
	writeFile(t, c, "C")

	first := fingerprintOf(
		t,
		root,
		a,
		b,
		c,
	)

	second := fingerprintOf(
		t,
		root,
		c,
		a,
		b,
	)

	if first != second {
		t.Fatalf(
			"expected identical fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}

func TestMultipleFilesChangeWhenOneChanges(t *testing.T) {
	root := t.TempDir()

	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")

	writeFile(t, a, "A")
	writeFile(t, b, "B")

	before := fingerprintOf(
		t,
		root,
		a,
		b,
	)

	writeFile(t, b, "different")

	after := fingerprintOf(
		t,
		root,
		a,
		b,
	)

	if before == after {
		t.Fatalf(
			"expected fingerprint to change\nbefore: %s\nafter:  %s",
			before,
			after,
		)
	}
}

func TestDirectoryReturnsError(t *testing.T) {
	root := t.TempDir()

	_, err := Fingerprint(root, root)

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEmptyInputDeterministic(t *testing.T) {
	root := t.TempDir()

	first := fingerprintOf(t, root)
	second := fingerprintOf(t, root)

	if first != second {
		t.Fatalf(
			"expected identical fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}

func TestSameContentsDifferentNames(t *testing.T) {
	root := t.TempDir()

	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")

	writeFile(t, a, "identical")
	writeFile(t, b, "identical")

	first := fingerprintOf(t, root, a)
	second := fingerprintOf(t, root, b)

	if first == second {
		t.Fatalf(
			"expected different fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}

func TestFingerprintIndependentOfWorkspaceLocation(t *testing.T) {
	root := t.TempDir()

	repo1 := filepath.Join(root, "repo1")
	repo2 := filepath.Join(root, "repo2")

	if err := os.Mkdir(repo1, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(repo2, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(
		t,
		filepath.Join(repo1, "package.json"),
		`{"name":"demo"}`,
	)

	writeFile(
		t,
		filepath.Join(repo2, "package.json"),
		`{"name":"demo"}`,
	)

	first := fingerprintOf(
		t,
		repo1,
		filepath.Join(repo1, "package.json"),
	)

	second := fingerprintOf(
		t,
		repo2,
		filepath.Join(repo2, "package.json"),
	)

	if first != second {
		t.Fatalf(
			"expected identical fingerprints\nfirst:  %s\nsecond: %s",
			first,
			second,
		)
	}
}
