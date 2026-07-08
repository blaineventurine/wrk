package executor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func detach(link, target string) error {
	tmp := link + ".wrk-tmp"
	backup := link + ".wrk-backup"

	// Pre-flight: refuse to touch a scratch path that already holds
	// something on disk. An unconditional RemoveAll here would silently
	// obliterate a user-authored `.wrk-tmp`/`.wrk-backup` sitting next
	// to the resource; copyPath below would then overwrite the tmp
	// contents before the operator noticed. On the happy path detach
	// creates and consumes both scratch paths inside this function's
	// own window — they never survive a successful run. If a prior
	// invocation crashed mid-swap and left them behind, aborting here
	// is the correct signal to the operator; recovery is manual (or
	// via a future `wrk gc`).
	//
	// The gitignore templates ship the `.wrk-tmp` / `.wrk-backup`
	// patterns so a stale artifact is at least invisible to VCS.
	if _, err := os.Lstat(tmp); err == nil {
		return fmt.Errorf(
			"refusing to detach %s: scratch path %s already exists (crashed prior detach or user data?); remove it and retry",
			link, tmp,
		)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf(
			"refusing to detach %s: backup path %s already exists (crashed prior detach or user data?); remove it and retry",
			link, backup,
		)
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := copyPath(target, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	// Move the symlink out of the way.
	if err := os.Rename(link, backup); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	// Move the copied resource into place.
	if err := os.Rename(tmp, link); err != nil {
		// Best-effort rollback.
		_ = os.Rename(backup, link)
		_ = os.RemoveAll(tmp)
		return err
	}

	// Best-effort cleanup.
	_ = os.RemoveAll(backup)

	return nil
}

func copyFile(
	source string,
	destination string,
) error {
	if err := os.MkdirAll(
		filepath.Dir(destination),
		0o755,
	); err != nil {
		return err
	}

	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(
		destination,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		info.Mode(),
	)
	if err != nil {
		return err
	}

	if _, err := io.Copy(
		out,
		in,
	); err != nil {
		_ = out.Close()
		return err
	}

	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}

	// Close is deliberately checked here instead of via defer: on some
	// filesystems the final flush-to-disk error is only surfaced by
	// Close(), and losing it would leave a silently truncated file.
	if err := out.Close(); err != nil {
		return err
	}

	return nil
}
