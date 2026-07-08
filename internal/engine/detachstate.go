package engine

import (
	"encoding/json"
	"fmt"
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
	path := registryPath(repo)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return detachRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	reg := detachRegistry{}
	if err := json.Unmarshal(data, &reg); err != nil {
		// Corruption is tolerated (starting from empty is safe: the
		// registry is accretive and only tracks user-declared detaches),
		// but a silent reset would hide a real problem. Log to stderr
		// so the operator sees the file that got wiped on next save.
		fmt.Fprintf(os.Stderr,
			"wrk: detach registry at %s is corrupt (%v), treating as empty\n",
			path, err)
		return detachRegistry{}, nil
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

	// Write atomically: truncating the real file mid-write on a crash
	// leaves invalid JSON on disk, which loadRegistry silently treats as
	// empty. Rendering the registry to a sibling tmp file first and then
	// renaming means the real path is only ever the old (valid) file or
	// the new (valid) file — never a truncated in-between.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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
