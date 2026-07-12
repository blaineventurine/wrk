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
// Hard links to the same underlying inode are counted only once, so
// onProgress reflects actual bytes reclaimed rather than N * size for
// N links. Without dedup, a resource whose fingerprint variant is
// built from hard-linked node_modules copies over-reports the freed
// bytes by a large multiple. The dedup runs on (device, inode) pairs
// derived from FileInfo.Sys(); a platform whose FileInfo cannot
// yield an inode falls back to the pre-fix per-link count so the
// progress bar never stalls.
//
// A nil onProgress is a valid no-op; callers can pass the zero
// value of Options.Progress unconditionally.
//
// About ~2x the syscall count of os.RemoveAll — the extra Lstat on
// every entry is what enables accurate per-file byte counting. That
// cost is only paid on operations that opted into progress reporting
// (wrk remove / gc / forget); other paths still use os.RemoveAll.
func RemoveAllProgress(path string, onProgress func(int64)) error {
	// Per-call inode set: dedup is scoped to a single removal so a
	// second invocation on an unrelated tree does not inherit any
	// counted-inode state. Nil callback skips inode bookkeeping
	// entirely — nothing to dedup against.
	var seen map[inodeKey]bool
	if onProgress != nil {
		seen = make(map[inodeKey]bool)
	}
	return removeAllProgress(path, onProgress, seen)
}

// inodeKey identifies a unique on-disk inode. The (device, inode)
// pair is the only correct dedup key for hard links: two paths sharing
// an inode share bytes; two paths on different devices with the same
// inode number are distinct files.
type inodeKey struct {
	dev uint64
	ino uint64
}

// removeAllProgress is the recursive core. seen may be nil (nil
// callback path), in which case inode bookkeeping is skipped.
func removeAllProgress(path string, onProgress func(int64), seen map[inodeKey]bool) error {
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
			if err := removeAllProgress(filepath.Join(path, e.Name()), onProgress, seen); err != nil {
				return err
			}
		}
		return os.Remove(path)
	}

	if info.Mode().IsRegular() && onProgress != nil {
		// Count each inode at most once. On platforms without inode
		// support (inodeKeyOf returns false), count every link — the
		// worst-case over-report matches the pre-fix behavior and is
		// still better than under-reporting.
		if key, ok := inodeKeyOf(info); ok {
			if !seen[key] {
				seen[key] = true
				onProgress(info.Size())
			}
		} else {
			onProgress(info.Size())
		}
	}
	return os.Remove(path)
}
