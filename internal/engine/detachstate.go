package engine

import (
	"encoding/json"
	"os"
	"path/filepath"

	"wrk/internal/repository"
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
func recordDetached(repo *repository.Repository, relPaths []string) error {
	reg, err := loadRegistry(repo)
	if err != nil {
		return err
	}
	reg[repo.Root] = relPaths
	return saveRegistry(repo, reg)
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
