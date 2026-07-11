package executor

import (
	"os"
	"path/filepath"
)

// RemoveAllProgress removes path and everything under it, calling
// onProgress with the size of each regular file removed. Missing
// path is not an error, matching os.RemoveAll's contract.
//
// The walk is post-order (leaves first, then containing dirs) so
// each directory is empty when the final os.Remove hits it. onProgress
// fires only for regular files; symlinks and directories contribute
// zero bytes (matching `du -sb --apparent-size` semantics). Symlinks
// are removed but never followed, so a link pointing outside the
// tree never trips a chain-of-deletion outside path.
//
// A nil onProgress is a valid no-op; callers can pass the zero
// value of Options.Progress unconditionally.
//
// About ~2x the syscall count of os.RemoveAll — the extra Lstat on
// every entry is what enables accurate per-file byte counting. That
// cost is only paid on operations that opted into progress reporting
// (wrk remove / gc / forget); other paths still use os.RemoveAll.
func RemoveAllProgress(path string, onProgress func(int64)) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Symlinks (even to directories) are removed at the current
	// level, never followed. Regular files add to the counter;
	// everything else (device nodes, sockets, named pipes) is
	// removed without counting bytes since du treats them as
	// negligible.
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := RemoveAllProgress(filepath.Join(path, e.Name()), onProgress); err != nil {
				return err
			}
		}
		return os.Remove(path)
	}

	if info.Mode().IsRegular() && onProgress != nil {
		onProgress(info.Size())
	}
	return os.Remove(path)
}
