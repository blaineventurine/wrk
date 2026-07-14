package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/gofrs/flock"

	"github.com/blaineventurine/wrk/internal/repository"
)

// cloneRegistry records every CLONE of a repository that has linked
// against a shared-storage root. Two independent clones of the same
// remote share `<storage>/<repo-id>/` by design (that is the
// cross-machine/cross-clone sharing feature), but each clone's VCS
// only enumerates its OWN worktrees — without this registry, `wrk gc`
// in clone A sees clone B's pinned variants as unused and deletes
// them, and `wrk forget` in A nukes storage B depends on.
//
// The registry is keyed by the clone's canonical metadata directory
// (the git common dir), which uniquely identifies a clone across all
// of its worktrees. The value carries the clone's primary workspace
// root — the anchor from which its live worktrees can be enumerated.
//
// Lifecycle: upserted on every successful link/relink and on gc/forget
// plan builds (so pre-registry clones self-register on first use);
// entries whose metadata dir no longer exists are pruned during reads;
// the whole file is removed by `wrk forget`.
type cloneRegistry map[string]cloneEntry

// cloneEntry is one registered clone.
type cloneEntry struct {
	// Root is the clone's primary workspace root at registration time.
	Root string `json:"root"`
	// UpdatedAt is a diagnostic RFC3339 timestamp. Not load-bearing.
	UpdatedAt string `json:"updatedAt"`
}

// clonesPath is the on-disk location of the clone registry: a SIBLING
// of the `<storage>/<repo-id>/` subtree (like the `.wrk-forgetting`
// marker), so the variant scan, orphan sweep, and forget's rename
// never see it as repo content.
func clonesPath(repo *repository.Repository, options Options) string {
	return filepath.Join(
		options.StorageRoot,
		repo.RepositoryID+".wrk-clones.json",
	)
}

// withClonesLock serializes load-modify-save cycles on the clone
// registry across processes AND across clones — the lock file lives in
// shared storage, the one filesystem location every clone sees.
func withClonesLock(repo *repository.Repository, options Options, fn func() error) error {
	lockPath := clonesPath(repo, options) + ".wrk-lock"
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

// loadClones reads the registry. Missing file → empty registry; a
// corrupt file is logged and treated as empty (matching the detach and
// isolation registries: starting empty is safe because the registry
// only widens gc/forget's view — the invoking clone's own workspaces
// are always enumerated directly).
func loadClones(repo *repository.Repository, options Options) (cloneRegistry, error) {
	path := clonesPath(repo, options)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cloneRegistry{}, nil
	}
	if err != nil {
		return nil, err
	}
	reg := cloneRegistry{}
	if err := json.Unmarshal(data, &reg); err != nil {
		fmt.Fprintf(os.Stderr,
			"wrk: clone registry at %s is corrupt (%v), treating as empty\n",
			path, err)
		return cloneRegistry{}, nil
	}
	if reg == nil {
		reg = cloneRegistry{}
	}
	return reg, nil
}

// saveClones writes the registry atomically (sibling tmp + rename).
// Callers MUST hold withClonesLock.
func saveClones(repo *repository.Repository, options Options, reg cloneRegistry) error {
	path := clonesPath(repo, options)
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
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

// canonicalMetadataDir is the registry key for repo's clone: the
// EvalSymlinks-resolved metadata (git common) directory, stable across
// every worktree of the clone and across /var vs /private/var
// spellings.
func canonicalMetadataDir(repo *repository.Repository) string {
	dir := repo.MetadataDir()
	if canon, err := filepath.EvalSymlinks(dir); err == nil {
		return canon
	}
	return dir
}

// RegisterClone upserts the invoking clone into the registry.
// Best-effort by contract: callers run it AFTER their real work
// succeeded, and a bookkeeping failure must not fail a successful
// link — surface it on stderr and move on. Registration is what makes
// gc/forget in OTHER clones aware of this one, so it runs on every
// link/relink and on gc/forget plan builds.
func registerClone(repo *repository.Repository, options Options) {
	workspaces, err := repo.Workspaces()
	if err != nil || len(workspaces) == 0 {
		return // cannot name a primary; nothing useful to record
	}
	primary := workspaces[0]
	if canon, err := filepath.EvalSymlinks(primary); err == nil {
		primary = canon
	}

	key := canonicalMetadataDir(repo)

	err = withClonesLock(repo, options, func() error {
		reg, err := loadClones(repo, options)
		if err != nil {
			return err
		}
		entry, exists := reg[key]
		if exists && entry.Root == primary {
			return nil // already current; skip the write
		}
		reg[key] = cloneEntry{
			Root:      primary,
			UpdatedAt: nowUTC(),
		}
		return saveClones(repo, options, reg)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"wrk: could not update clone registry: %v\n", err)
	}
}

// otherCloneRoots returns the live workspace roots of every OTHER
// registered clone of this repository, plus a list of unreachable
// markers for clones whose state could not be enumerated. Callers MUST
// treat a non-empty unreachable list conservatively (keep every
// variant, skip the orphan sweep) — an unenumerable clone may pin
// anything.
//
// Entries whose metadata dir has vanished (the clone was deleted) are
// pruned from the registry as a side effect: a gone clone pins
// nothing, and pruning keeps the registry from accreting forever.
func otherCloneRoots(repo *repository.Repository, options Options) (roots, unreachable []string) {
	reg, err := loadClones(repo, options)
	if err != nil {
		return nil, []string{clonesPath(repo, options) + " (unreadable)"}
	}

	own := canonicalMetadataDir(repo)

	var dead []string
	keys := make([]string, 0, len(reg))
	for key := range reg {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if key == own {
			continue
		}
		entry := reg[key]

		if _, statErr := os.Stat(key); os.IsNotExist(statErr) {
			dead = append(dead, key)
			continue // clone deleted — pins nothing
		}

		otherRepo, err := repository.Detect(entry.Root, repo.VCS())
		if err != nil {
			unreachable = append(unreachable, entry.Root)
			continue
		}
		// Guard against a re-created directory at the recorded root
		// that belongs to a DIFFERENT clone (or repo) now.
		if canonicalMetadataDir(otherRepo) != key {
			dead = append(dead, key)
			continue
		}

		workspaces, err := otherRepo.Workspaces()
		if err != nil {
			unreachable = append(unreachable, entry.Root)
			continue
		}
		ghosts, err := otherRepo.DetectGhosts()
		if err != nil {
			unreachable = append(unreachable, entry.Root)
			continue
		}
		roots = append(roots, filterOutGhosts(workspaces, ghosts)...)
	}

	if len(dead) > 0 {
		_ = withClonesLock(repo, options, func() error {
			reg, err := loadClones(repo, options)
			if err != nil {
				return err
			}
			changed := false
			for _, key := range dead {
				if _, ok := reg[key]; ok {
					delete(reg, key)
					changed = true
				}
			}
			if !changed {
				return nil
			}
			return saveClones(repo, options, reg)
		})
	}

	return roots, unreachable
}

// otherCloneRootsStrict is the recheck-side variant: any doubt —
// unreadable registry, unreachable clone — surfaces as an error so
// deleteVariant/deleteOrphanedTree keep the data (conservative).
func otherCloneRootsStrict(repo *repository.Repository, options Options) ([]string, error) {
	roots, unreachable := otherCloneRoots(repo, options)
	if len(unreachable) > 0 {
		return nil, fmt.Errorf(
			"clone(s) sharing this storage could not be enumerated: %v",
			unreachable,
		)
	}
	return roots, nil
}

// removeClonesFile deletes the registry and its lock file. Called by
// ExecuteForget after the storage subtree is gone — the registry
// describes clones of a repository whose storage no longer exists.
func removeClonesFile(repo *repository.Repository, options Options) {
	_ = os.Remove(clonesPath(repo, options))
	_ = os.Remove(clonesPath(repo, options) + ".wrk-lock")
}
