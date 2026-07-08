package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/location"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// ResourceListing describes a configured resource and its shared storage.
type ResourceListing struct {
	Resource      string
	Path          string // workspace-relative path
	Fingerprinted bool

	// SharedPath is the storage location for the current workspace's
	// fingerprint (or the single shared copy when not fingerprinted).
	SharedPath string

	// Variants is the number of fingerprint directories on disk for this
	// resource. It is 1 (or 0) for non-fingerprinted resources.
	Variants int

	// Size is the total bytes under the resource's shared storage subtree.
	// Only populated when withSize is requested; -1 otherwise.
	Size int64

	Origin config.Origin
}

// List reports the configured resources and their shared storage for the
// current workspace's repository. It never mutates anything.
//
// When withSize is true, each listing includes the on-disk size of its
// shared storage subtree (which requires walking the tree and can be slow).
func List(
	repo *repository.Repository,
	options Options,
	withSize bool,
) ([]ResourceListing, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, err
	}
	printWarnings(cfg, options.Stdout)

	var listings []ResourceListing

	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(repo.Root, resource)
		if err != nil {
			return nil, err
		}

		for _, instance := range instances {
			loc, err := location.For(
				options.StorageRoot,
				repo.RepositoryID,
				instance,
			)
			if err != nil {
				return nil, err
			}

			fingerprinted := len(instance.FingerprintInputs) > 0

			// The subtree that holds this resource's shared copies. For a
			// fingerprinted resource that is the parent of the fingerprint
			// dir; otherwise it is the shared path itself.
			subtree := loc.Path
			if fingerprinted {
				subtree = filepath.Dir(loc.Path)
			}

			listing := ResourceListing{
				Resource:      instance.Resource.Name,
				Path:          instance.RelativePath,
				Fingerprinted: fingerprinted,
				SharedPath:    loc.Path,
				Variants:      countVariants(subtree, fingerprinted),
				Size:          -1,
				Origin:        instance.Resource.Origin,
			}

			if withSize {
				size, err := treeSize(subtree)
				if err != nil {
					return nil, err
				}
				listing.Size = size
			}

			listings = append(listings, listing)
		}
	}

	return listings, nil
}

// countVariants counts the shared copies present on disk.
//
// For a fingerprinted resource, that is the number of fingerprint
// subdirectories. For a non-fingerprinted resource, it is 1 if the shared
// path exists, else 0.
func countVariants(subtree string, fingerprinted bool) int {
	if !fingerprinted {
		if _, err := os.Lstat(subtree); err == nil {
			return 1
		}
		return 0
	}

	entries, err := os.ReadDir(subtree)
	if err != nil {
		return 0 // storage not created yet
	}

	count := 0
	for _, e := range entries {
		// Ignore wrk's bookkeeping files (locks, temp/backup dirs).
		name := e.Name()
		if isBookkeeping(name) {
			continue
		}
		if e.IsDir() {
			count++
		}
	}
	return count
}

func isBookkeeping(name string) bool {
	switch {
	case name == ".wrk-lock",
		strings.HasSuffix(name, ".wrk-lock"),
		strings.HasSuffix(name, ".wrk-tmp"),
		strings.HasSuffix(name, ".wrk-backup"):
		return true
	}
	return false
}

// treeSize returns the total size in bytes of all regular files under root.
// Symlinks are not followed. A missing root yields size 0.
//
// Errors reading individual entries (missing files, permission denied,
// stat failures) are tolerated: the walk continues and the affected
// bytes are simply omitted from the reported total. The returned size is
// therefore a lower bound on real usage in the face of partial access,
// which is preferable to failing the whole `wrk list --size` command.
func treeSize(root string) (int64, error) {
	var total int64

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if errors.Is(err, fs.ErrPermission) {
				// Skip the whole subtree we can't descend into.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			// Other transient errors (stale fs handles, etc.) are also
			// tolerated: reporting a slightly-low size beats aborting.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			// File vanished, permission dropped between readdir and
			// stat — skip and keep counting the rest.
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}

	return total, nil
}
