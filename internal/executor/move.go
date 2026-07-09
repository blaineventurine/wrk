package executor

import (
	"errors"
	"fmt"
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

	// The copy is safely in place; remove the original. If we crash
	// here, source and destination both hold identical bytes. The next
	// Execute Move takes the idempotent-completion recovery path in
	// execute.go: sameContents matches, source is removed, swap done.
	// Message the user for the same-workspace recovery: `wrk relink`
	// is what discards a redundant source; `wrk link` would refuse
	// with a conflict because both sides look "provisioned".
	if err := os.RemoveAll(source); err != nil {
		return fmt.Errorf(
			"moved to shared storage at %s but failed to remove source %s (run `wrk relink` inside the workspace to complete the swap; any edits you make to %s meanwhile will be discarded): %w",
			destination, source, source, err,
		)
	}

	return nil
}

// Move relocates source to destination. It first attempts an atomic
// rename; on cross-device failure it falls back to a staged copy so
// a partial copy never replaces the source.
//
// Exported wrapper around move; used by engine.RelinkIsolate to
// migrate a workspace's detached copy into shared storage. Prefer
// this over hand-rolling filesystem moves — the cross-device path is
// tested and the failure semantics are load-bearing.
func Move(source, destination string) error {
	return move(source, destination)
}

// copyPath copies a file or directory from source to destination.
//
// Uses Lstat rather than Stat so a symlink at source is detected and
// refused: silently following the link could copy content from outside
// the intended tree.
func copyPath(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to copy through symlink: %s", source)
	}

	if info.IsDir() {
		return dircopy.Copy(source, destination)
	}

	return copyFile(source, destination)
}

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
