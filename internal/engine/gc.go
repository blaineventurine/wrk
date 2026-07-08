package engine

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/repository"
	"github.com/blaineventurine/wrk/internal/resolver"
)

// variant describes one shared copy of a resource sitting on disk. For
// fingerprinted resources the Fingerprint field holds the subdir name;
// for un-fingerprinted resources Fingerprint is empty and StoragePath
// points directly at the resource dir.
type variant struct {
	Resource    string    // config resource name (e.g. "node")
	Path        string    // resource repo-relative path (e.g. "node_modules")
	Fingerprint string    // subdir name; "" for un-fingerprinted resources
	StoragePath string    // absolute path to the variant on disk
	Size        int64     // bytes; walks the subtree via treeSize
	LastUsed    time.Time // subdir mtime (Stat().ModTime())
}

// scanVariants enumerates every fingerprint variant currently present
// on disk for every configured resource in repo. Un-fingerprinted
// resources produce at most one variant with an empty Fingerprint. The
// function is read-only; it never writes to storage.
//
// Resources whose shared subtree does not exist yet contribute zero
// variants; scanVariants does NOT treat that as an error.
func scanVariants(repo *repository.Repository, options Options) ([]variant, error) {
	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, err
	}

	var variants []variant

	for _, resource := range cfg.Resources {
		instances, err := resolver.Resolve(repo.Root, resource)
		if err != nil {
			return nil, err
		}

		for _, instance := range instances {
			fingerprinted := len(instance.FingerprintInputs) > 0
			// For fingerprinted resources this is the parent that holds
			// each variant subdir; for un-fingerprinted resources it is
			// the single shared copy itself.
			subtree := filepath.Join(
				options.StorageRoot,
				repo.RepositoryID,
				instance.RelativePath,
			)

			if !fingerprinted {
				// Un-fingerprinted: at most one variant, keyed by
				// existence of the shared path itself. Missing storage
				// is not an error — it just means Link has never run.
				if _, err := os.Lstat(subtree); err != nil {
					continue
				}
				variants = append(variants, newVariant(
					instance.Resource.Name,
					instance.RelativePath,
					"",
					subtree,
				))
				continue
			}

			entries, err := os.ReadDir(subtree)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			for _, entry := range entries {
				name := entry.Name()
				if isBookkeeping(name) {
					continue
				}
				if !entry.IsDir() {
					continue
				}
				variants = append(variants, newVariant(
					instance.Resource.Name,
					instance.RelativePath,
					name,
					filepath.Join(subtree, name),
				))
			}
		}
	}

	return variants, nil
}

// newVariant populates a variant with best-effort Size and LastUsed.
// treeSize/Stat failures don't abort the read-only sweep — a variant
// with Size=0 or LastUsed=zero is still more useful to the plan builder
// than a whole aborted scan.
func newVariant(resource, path, fingerprint, storagePath string) variant {
	v := variant{
		Resource:    resource,
		Path:        path,
		Fingerprint: fingerprint,
		StoragePath: storagePath,
	}
	if size, err := treeSize(storagePath); err == nil {
		v.Size = size
	}
	if info, err := os.Stat(storagePath); err == nil {
		v.LastUsed = info.ModTime()
	}
	return v
}

// pinnedVariants returns the set of variant storage paths (absolute)
// currently symlinked from a live workspace of repo. Unreachable
// workspace roots (Stat failure on the root itself) are treated
// conservatively — every scanned variant is marked pinned to avoid
// deleting data referenced from a workspace we cannot inspect. The
// unreachable roots are returned so the plan builder can surface why
// the sweep was conservative.
//
// Non-managed symlinks (targets outside any scanned variant) do not
// appear in the pinned set.
func pinnedVariants(
	repo *repository.Repository,
	options Options,
) (map[string]bool, []string, error) {
	variants, err := scanVariants(repo, options)
	if err != nil {
		return nil, nil, err
	}

	cfg, err := config.Load(repo.Root)
	if err != nil {
		return nil, nil, err
	}

	workspaces, err := repo.Workspaces()
	if err != nil {
		return nil, nil, err
	}

	pinned := make(map[string]bool)
	var unreachable []string

	pinAll := func() {
		for _, v := range variants {
			pinned[v.StoragePath] = true
		}
	}

	for _, workspaceRoot := range workspaces {
		if _, err := os.Stat(workspaceRoot); err != nil {
			unreachable = append(unreachable, workspaceRoot)
			pinAll()
			continue
		}

		for _, resource := range cfg.Resources {
			// Use the workspace's OWN root so {root}-anchored globs and
			// paths expand against the workspace under inspection.
			instances, err := resolver.Resolve(workspaceRoot, resource)
			if err != nil {
				return nil, nil, err
			}

			for _, instance := range instances {
				info, err := os.Lstat(instance.WorkspacePath)
				if err != nil {
					continue
				}
				if info.Mode()&os.ModeSymlink == 0 {
					continue
				}

				resolved, err := filepath.EvalSymlinks(instance.WorkspacePath)
				if err != nil {
					continue
				}

				for _, v := range variants {
					if isPathInside(v.StoragePath, resolved) {
						pinned[v.StoragePath] = true
						break
					}
				}
			}
		}
	}

	return pinned, unreachable, nil
}

// isPathInside reports whether target is base or lives inside base.
// Both must be absolute paths; filepath.Rel keeps the check honest
// across differing but equivalent spellings.
func isPathInside(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
