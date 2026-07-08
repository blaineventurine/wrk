package executor

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// sameContents reports whether two filesystem paths hold byte-identical
// content. Regular files are compared by contents; directories are
// compared by walking both trees and hashing per-entry (relative path,
// kind, and content). Symlinks are compared by their link target.
//
// Called from the Move idempotent-completion recovery: when the shared
// destination already exists, we use sameContents to distinguish a
// crashed-mid-swap state (source and destination match byte-for-byte)
// from a genuine race with a peer whose provisioning produced different
// output. Only the former is safe to complete silently.
func sameContents(a, b string) (bool, error) {
	infoA, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	infoB, err := os.Lstat(b)
	if err != nil {
		return false, err
	}

	// Any mode-kind divergence (dir vs. file, symlink vs. real) is
	// enough to declare "not the same" without looking at bytes.
	if infoA.Mode().Type() != infoB.Mode().Type() {
		return false, nil
	}

	switch {
	case infoA.Mode()&os.ModeSymlink != 0:
		targetA, err := os.Readlink(a)
		if err != nil {
			return false, err
		}
		targetB, err := os.Readlink(b)
		if err != nil {
			return false, err
		}
		return targetA == targetB, nil

	case infoA.IsDir():
		hashA, err := hashTree(a)
		if err != nil {
			return false, err
		}
		hashB, err := hashTree(b)
		if err != nil {
			return false, err
		}
		return bytes.Equal(hashA, hashB), nil

	case infoA.Mode().IsRegular():
		if infoA.Size() != infoB.Size() {
			return false, nil
		}
		return sameFileContents(a, b)

	default:
		// Devices, sockets, named pipes, etc. Recovery does not
		// meaningfully apply; refuse to claim identity.
		return false, nil
	}
}

// hashTree walks root without following symlinks and returns a hash
// covering every entry's relative path, kind, and content (or symlink
// target). Two directory trees that hash equal are safe to treat as
// byte-identical for the recovery check.
func hashTree(root string) ([]byte, error) {
	hasher := sha256.New()

	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		// Domain-separate path, kind, and body so a file whose contents
		// happen to spell a directory name never collides with an
		// actual directory of that name.
		fmt.Fprintf(hasher, "P:%s\x00", rel)

		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(hasher, "L:%s\x00", target)

		case info.IsDir():
			fmt.Fprint(hasher, "D:\x00")

		case info.Mode().IsRegular():
			fmt.Fprint(hasher, "F:")
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			if _, err := io.Copy(hasher, f); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			fmt.Fprint(hasher, "\x00")

		default:
			fmt.Fprintf(hasher, "O:%s\x00", info.Mode().String())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}

// sameFileContents streams two regular files and returns true when
// their bytes match. Callers must have already confirmed both paths
// exist and have equal size.
func sameFileContents(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer func() { _ = fa.Close() }()

	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer func() { _ = fb.Close() }()

	const chunk = 64 * 1024
	bufA := make([]byte, chunk)
	bufB := make([]byte, chunk)
	for {
		nA, errA := io.ReadFull(fa, bufA)
		nB, errB := io.ReadFull(fb, bufB)
		if nA != nB || !bytes.Equal(bufA[:nA], bufB[:nB]) {
			return false, nil
		}
		if errA == io.EOF || errA == io.ErrUnexpectedEOF {
			// Both drained together (nA == nB and equal so far).
			if errB == io.EOF || errB == io.ErrUnexpectedEOF {
				return true, nil
			}
			return false, errB
		}
		if errA != nil {
			return false, errA
		}
		if errB != nil {
			return false, errB
		}
	}
}
