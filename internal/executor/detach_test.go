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

// TestCopyFileCopiesContentAndMode pins the two-clause contract of
// copyFile: the destination's byte content equals the source and the
// destination's mode equals the source's mode. Mode preservation
// matters because copyFile is the workhorse of both detach (writes a
// real file where a symlink used to be) and move's cross-device
// fallback (has to reproduce the original bits and permissions of
// user code).
func TestCopyFileCopiesContentAndMode(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src")
	// Pick a mode that survives the process umask (0o022 by default):
	// 0o600 has no group/other bits so the umask does not tighten it.
	const wantMode os.FileMode = 0o600
	if err := os.WriteFile(source, []byte("payload-bytes"), wantMode); err != nil {
		t.Fatal(err)
	}
	// os.WriteFile respects umask; re-chmod to pin the mode
	// unambiguously.
	if err := os.Chmod(source, wantMode); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(dir, "dst")

	if err := copyFile(source, destination); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if string(got) != "payload-bytes" {
		t.Errorf("copyFile content = %q, want %q", got, "payload-bytes")
	}

	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat destination: %v", err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Errorf("copyFile mode = %o, want %o", got, wantMode)
	}
}

// TestCopyFileFailsOnMissingSource pins the "source must exist"
// contract: copyFile does not silently produce an empty destination
// when source is missing — it surfaces the open error. A regression
// that swallowed the error would leave an empty file in place of the
// intended one.
func TestCopyFileFailsOnMissingSource(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "does-not-exist")
	destination := filepath.Join(dir, "dst")

	err := copyFile(source, destination)
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected IsNotExist, got %v", err)
	}

	// Destination must NOT have been created.
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("expected no destination, got err=%v", err)
	}
}

// TestCopyFileCreatesDestParent pins the MkdirAll dance copyFile does
// on the destination's parent. Detach and move both target paths
// under freshly-provisioned shared trees where the intermediate dirs
// don't exist yet; requiring callers to MkdirAll first would spread
// that responsibility unhelpfully.
func TestCopyFileCreatesDestParent(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src")
	if err := os.WriteFile(source, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Two levels of missing parents so plain os.Mkdir would fail.
	destination := filepath.Join(dir, "nested", "deeper", "dst")

	if err := copyFile(source, destination); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if string(got) != "body" {
		t.Errorf("content = %q, want %q", got, "body")
	}
}

// TestDetachFailsWhenLinkAbsent pins the second-Rename error path in
// detach: after copying target→tmp, moving the link out of the way
// requires the link to actually exist. If it doesn't, detach must
// surface the error and leave nothing behind.
func TestDetachFailsWhenLinkAbsent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "shared")
	writeFile(t, target, "body")

	link := filepath.Join(root, "not-yet")

	err := detach(link, target)
	if err == nil {
		t.Fatal("expected detach to fail when link is absent, got nil")
	}

	// The staged tmp copy must have been cleaned up.
	if _, err := os.Lstat(link + ".wrk-tmp"); !os.IsNotExist(err) {
		t.Errorf("expected tmp cleanup, got err=%v", err)
	}
}

// TestCopyFileMkdirAllFails pins the guard at the top of copyFile: a
// destination whose parent chain crosses a regular file must not
// proceed to open the source or truncate the destination. copyFile
// surfaces the MkdirAll error.
func TestCopyFileMkdirAllFails(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Regular file at what would need to be a mid-path directory.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(blocker, "child", "dst")

	err := copyFile(source, destination)
	if err == nil {
		t.Fatal("expected copyFile to surface MkdirAll error, got nil")
	}
	// Blocker file untouched (nothing was opened or written).
	if got, err := os.ReadFile(blocker); err != nil {
		t.Errorf("blocker file removed on failure: err=%v", err)
	} else if string(got) != "in the way" {
		t.Errorf("blocker mutated on failure: got %q", got)
	}
}

// TestCopyFileDestinationIsDirectoryFails pins the OpenFile error
// path: when the destination path is an existing directory,
// OpenFile(O_CREATE|O_WRONLY|O_TRUNC) fails with EISDIR, and
// copyFile surfaces the error without touching the source or the
// directory's contents.
func TestCopyFileDestinationIsDirectoryFails(t *testing.T) {
	dir := t.TempDir()

	source := filepath.Join(dir, "src")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Destination path is a real directory containing a file we can
	// verify remained untouched.
	destination := filepath.Join(dir, "dst-dir")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "sentinel")
	if err := os.WriteFile(sentinel, []byte("dontclobber"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(source, destination); err == nil {
		t.Fatal("expected copyFile to reject directory destination, got nil")
	}

	// Directory and its contents survive.
	if info, err := os.Stat(destination); err != nil {
		t.Errorf("destination directory removed: err=%v", err)
	} else if !info.IsDir() {
		t.Errorf("destination shape changed: mode=%v", info.Mode())
	}
	if got, err := os.ReadFile(sentinel); err != nil {
		t.Errorf("sentinel gone: err=%v", err)
	} else if string(got) != "dontclobber" {
		t.Errorf("sentinel mutated: got %q", got)
	}
}

// TestDetachDoesNotWipeUserFileAtScratchPath pins H3: a user's
// unrelated file at `<link>.wrk-tmp` (or `.wrk-backup`) MUST survive a
// detach attempt. Prior to the fix, detach unconditionally
// `os.RemoveAll`'d both scratch paths at the top of every invocation,
// silently obliterating anything that happened to share the exact
// naming convention. The post-fix contract is: leave the scratch path
// alone, let the rename fail loudly if the operator's crash-recovery
// hasn't been done, and preserve every byte the user authored.
func TestDetachDoesNotWipeUserFileAtScratchPath(t *testing.T) {
	root := t.TempDir()

	shared := filepath.Join(root, "shared.env")
	writeFile(t, shared, "shared-content")

	link := filepath.Join(root, ".env")
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	// User's own file at the scratch name detach would have wiped.
	// The exact bytes must survive whether detach succeeds or fails.
	userTmp := link + ".wrk-tmp"
	const userPayload = "user-authored-do-not-touch"
	writeFile(t, userTmp, userPayload)

	// Detach may succeed or fail depending on filesystem semantics
	// (rename-onto-existing behavior). Both are acceptable — what is
	// NOT acceptable is a silent clobber of the user's file. Assert
	// only on the byte-preservation invariant.
	_ = detach(link, shared)

	got, err := os.ReadFile(userTmp)
	if err != nil {
		t.Fatalf("user's %s vanished during detach: %v", userTmp, err)
	}
	if string(got) != userPayload {
		t.Errorf("user's %s clobbered: got %q, want %q", userTmp, got, userPayload)
	}
}

// TestDetachDoesNotWipeUserFileAtBackupPath is the sibling case for
// `<link>.wrk-backup`. The pre-fix code called RemoveAll on both
// scratch names in the same top-of-function block; a regression that
// re-added either one would flip this test.
func TestDetachDoesNotWipeUserFileAtBackupPath(t *testing.T) {
	root := t.TempDir()

	shared := filepath.Join(root, "shared.env")
	writeFile(t, shared, "shared-content")

	link := filepath.Join(root, ".env")
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	userBackup := link + ".wrk-backup"
	const userPayload = "user-backup-do-not-touch"
	writeFile(t, userBackup, userPayload)

	_ = detach(link, shared)

	got, err := os.ReadFile(userBackup)
	if err != nil {
		t.Fatalf("user's %s vanished during detach: %v", userBackup, err)
	}
	if string(got) != userPayload {
		t.Errorf("user's %s clobbered: got %q, want %q", userBackup, got, userPayload)
	}
}
