package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const Length = 16

// Domain-separator tags written after the path separator to distinguish
// present files from missing ones. Without this a present file whose
// contents happen to be the byte string "MISSING" would collide with a
// same-named missing file.
const (
	tagMissing byte = 0x00
	tagPresent byte = 0x01
)

var separator = []byte{0}

// Fingerprint computes a deterministic fingerprint for a collection of files.
//
// The fingerprint depends on:
//
//   - repository-relative paths
//   - file contents
//   - whether files exist (distinguished from present files with any
//     contents, including the literal bytes "MISSING")
//
// Absolute filesystem paths are intentionally ignored so that multiple
// workspaces for the same repository produce identical fingerprints.
func Fingerprint(
	root string,
	paths ...string,
) (string, error) {
	hasher := sha256.New()

	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)

	for _, path := range sorted {
		if err := updatePath(
			hasher,
			root,
			path,
		); err != nil {
			return "", err
		}
	}

	sum := hex.EncodeToString(
		hasher.Sum(nil),
	)

	return sum[:Length], nil
}

func updatePath(
	hasher hash.Hash,
	root string,
	path string,
) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		relative = path
	}

	hasher.Write([]byte(filepath.ToSlash(relative)))
	hasher.Write(separator)

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			hasher.Write([]byte{tagMissing})
			return nil
		}

		return err
	}

	if info.IsDir() {
		return fmt.Errorf(
			"cannot fingerprint directory: %s",
			path,
		)
	}

	hasher.Write([]byte{tagPresent})

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	if _, err := io.Copy(hasher, f); err != nil {
		return err
	}

	return nil
}
