package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

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
// This is called BEFORE the executor runs the detach plan: the argument
// is the caller's INTENT (the workspace-relative paths the plan wants to
// materialize), not a filesystem-observed fact. Recording before execution
// means a mid-plan failure — or a SIGKILL between the executor's final
// swap and the caller's return — can never strand a real detached file
// with no registry entry, which `wrk status` would misclassify as
// StateConflict and `wrk relink` would then destroy.
//
// The registry is accretive: paths are unioned with any prior entry so a
// no-op detach (nothing new to detach) never wipes existing records, and
// a partially-executed plan safely leaves the full intent recorded — the
// next `wrk detach` completes any planned-but-unexecuted paths. Only
// clearDetached removes an entry.
//
// The load-through-save cycle is guarded by an OS-level flock on
// `<registryPath>.wrk-lock` (see withRegistryLock) so two workspaces of
// the same repo — which share `.git/wrk/detached.json` via
// `git --git-common-dir` — cannot race each other's atomic rename and
// silently drop an entry.
func recordDetached(repo *repository.Repository, relPaths []string) error {
	return withRegistryLock(repo, func() error {
		reg, err := loadRegistry(repo)
		if err != nil {
			return err
		}
		reg.union(repo.Root, relPaths)
		return saveRegistry(repo, reg)
	})
}

// withRegistryLock serializes the registry's load-through-save cycle
// across processes. Two workspaces of the same repo share
// `.git/wrk/detached.json` via `git --git-common-dir`, so concurrent
// `wrk detach` calls would otherwise interleave: both load, both modify,
// both atomically rename their tmp file, and the second rename replaces
// the first — silently dropping the first workspace's entry.
//
// The lock file lives next to the registry and is created lazily. Because
// it is an OS-level flock, the kernel releases it on process exit, so no
// stale-lock recovery is required.
func withRegistryLock(repo *repository.Repository, fn func() error) error {
	lockPath := registryPath(repo) + ".wrk-lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()
	return fn()
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
//
// Load-through-save is guarded by the same flock as recordDetached so a
// concurrent record on a sibling workspace cannot be overwritten.
func clearDetached(repo *repository.Repository) error {
	return withRegistryLock(repo, func() error {
		reg, err := loadRegistry(repo)
		if err != nil {
			return err
		}
		if _, ok := reg[repo.Root]; !ok {
			return nil
		}
		delete(reg, repo.Root)
		return saveRegistry(repo, reg)
	})
}

func isDetached(reg detachRegistry, root, relPath string) bool {
	for _, p := range reg[root] {
		if p == relPath {
			return true
		}
	}
	return false
}
