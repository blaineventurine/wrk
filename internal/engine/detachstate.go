package engine

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/blaineventurine/wrk/internal/repository"
)

// detachRegistry maps a workspace root to the relative resource paths that
// were detached (materialized as independent copies) in that workspace.
type detachRegistry map[string][]string

func registryPath(repo *repository.Repository) string {
	return filepath.Join(repo.MetadataDir(), "wrk", "detached.json")
}

func loadRegistry(repo *repository.Repository) (detachRegistry, error) {
	data, err := os.ReadFile(registryPath(repo))
	if os.IsNotExist(err) {
		return detachRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	reg := detachRegistry{}
	if err := json.Unmarshal(data, &reg); err != nil {
		return detachRegistry{}, nil // tolerate corruption; treat as empty
	}
	return reg, nil
}

func saveRegistry(repo *repository.Repository, reg detachRegistry) error {
	path := registryPath(repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// recordDetached marks the given relative paths as detached for repo.Root.
//
// The registry is accretive: paths are unioned with any prior entry so a
// no-op detach (nothing new to detach) never wipes existing records. Only
// clearDetached removes an entry.
func recordDetached(repo *repository.Repository, relPaths []string) error {
	reg, err := loadRegistry(repo)
	if err != nil {
		return err
	}
	reg.union(repo.Root, relPaths)
	return saveRegistry(repo, reg)
}

// union merges add into the entry for root, preserving order (existing
// paths first, then new ones) and dropping duplicates. A no-op call
// against a missing entry leaves the registry untouched.
func (r detachRegistry) union(root string, add []string) {
	existing := r[root]
	if len(existing) == 0 && len(add) == 0 {
		return
	}
	seen := make(map[string]bool, len(existing)+len(add))
	merged := make([]string, 0, len(existing)+len(add))
	for _, p := range existing {
		if !seen[p] {
			seen[p] = true
			merged = append(merged, p)
		}
	}
	for _, p := range add {
		if !seen[p] {
			seen[p] = true
			merged = append(merged, p)
		}
	}
	r[root] = merged
}

// clearDetached removes any detached record for repo.Root.
func clearDetached(repo *repository.Repository) error {
	reg, err := loadRegistry(repo)
	if err != nil {
		return err
	}
	if _, ok := reg[repo.Root]; !ok {
		return nil
	}
	delete(reg, repo.Root)
	return saveRegistry(repo, reg)
}

func isDetached(reg detachRegistry, root, relPath string) bool {
	for _, p := range reg[root] {
		if p == relPath {
			return true
		}
	}
	return false
}
