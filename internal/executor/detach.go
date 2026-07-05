package executor

import (
	"io"
	"os"
	"path/filepath"

	dircopy "github.com/otiai10/copy"
)

func detach(
	link string,
	target string,
) error {
	tmp := link + ".wrk-tmp"
	backup := link + ".wrk-backup"

	// Clean up anything left behind by a previous failed run.
	_ = os.RemoveAll(tmp)
	_ = os.RemoveAll(backup)

	info, err := os.Stat(target)
	if err != nil {
		return err
	}

	if info.IsDir() {
		if err := dircopy.Copy(
			target,
			tmp,
		); err != nil {
			return err
		}
	} else {
		if err := copyFile(
			target,
			tmp,
		); err != nil {
			return err
		}
	}

	// Move the symlink out of the way.
	if err := os.Rename(
		link,
		backup,
	); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	// Move the copied resource into place.
	if err := os.Rename(
		tmp,
		link,
	); err != nil {
		// Best-effort rollback.
		_ = os.Rename(
			backup,
			link,
		)

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
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(
		out,
		in,
	); err != nil {
		return err
	}

	if err := out.Sync(); err != nil {
		return err
	}

	return nil
}
