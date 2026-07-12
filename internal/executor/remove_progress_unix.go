//go:build unix

package executor

import (
	"os"
	"syscall"
)

// inodeKeyOf extracts a (device, inode) pair from a Unix FileInfo.
// wrk targets Unix-only environments (git/jj + fs semantics), so the
// syscall.Stat_t cast is the load-bearing identity: two hard links
// share Dev and Ino, two files on different mounts do not, and a
// tmpfile deleted-and-recreated between two Lstat calls will get a
// fresh Ino from the kernel.
//
// The `ok` return exists to keep the caller portable: any platform
// whose FileInfo.Sys() does not surface *syscall.Stat_t returns
// false, and RemoveAllProgress falls back to per-link counting so the
// progress bar never stalls.
func inodeKeyOf(info os.FileInfo) (inodeKey, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return inodeKey{}, false
	}
	return inodeKey{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}
