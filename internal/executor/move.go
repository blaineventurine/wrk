package executor

import (
	"errors"
	"os"
	"syscall"

	dircopy "github.com/otiai10/copy"
)

// move relocates source to destination.
//
// It first attempts an atomic rename. If source and destination live on
// different filesystems (rename returns EXDEV), it falls back to a
// copy-then-remove, staging the copy in a temporary location so a partial
// copy never replaces the source.
func move(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}

	tmp := destination + ".wrk-tmp"

	// Clean up anything left behind by a previous failed run.
	_ = os.RemoveAll(tmp)

	if err := copyPath(source, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	if err := os.Rename(tmp, destination); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}

	// The copy is safely in place; remove the original.
	if err := os.RemoveAll(source); err != nil {
		return err
	}

	return nil
}

// copyPath copies a file or directory from source to destination.
func copyPath(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return dircopy.Copy(source, destination)
	}

	return copyFile(source, destination)
}

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
