package resolver

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
)

// Resolve expands a configured resource into one or more concrete resource
// instances, matching glob paths against the workspace filesystem only.
//
// Callers that know the repository's shared-storage subtree should prefer
// ResolveWithStorage so glob resources provisioned by peer workspaces are
// visible even before this workspace materializes the matching paths.
func Resolve(
	root string,
	resource config.Resource,
) ([]ResourceInstance, error) {
	return ResolveWithStorage(root, "", resource)
}

// ResolveWithStorage expands a configured resource into one or more
// concrete resource instances.
//
// Non-glob paths yield exactly one instance rooted at the workspace.
//
// Glob paths are matched against BOTH the workspace filesystem and (when
// storageResourceRoot is non-empty) the repository's shared-storage
// subtree `<storage>/<repo-id>`. The storage side exists because
// filepath.Glob can only ever match paths that already exist: a fresh
// workspace (`wrk new`) has no `packages/*/node_modules` directories yet,
// so a workspace-only glob silently resolves to zero instances and the
// workspace is never provisioned. Storage-side matches are relpaths a
// peer workspace has already provisioned; they are re-anchored under
// root so the returned instances always describe workspace paths.
//
// The union is deduplicated by repository-relative path and sorted so
// plan output is deterministic regardless of which side matched first.
// Storage bookkeeping entries (`*.wrk-tmp`, `*.wrk-deleting`, ...) are
// never treated as resource matches.
func ResolveWithStorage(
	root string,
	storageResourceRoot string,
	resource config.Resource,
) ([]ResourceInstance, error) {
	var workspacePaths []string

	isGlobPath := isGlob(resource.Path)

	if isGlobPath {
		matches, err := filepath.Glob(
			filepath.Join(root, resource.Path),
		)
		if err != nil {
			return nil, err
		}

		workspacePaths = matches

		if storageResourceRoot != "" {
			storagePaths, err := storageGlobMatches(
				root, storageResourceRoot, resource.Path,
			)
			if err != nil {
				return nil, err
			}
			workspacePaths = unionPaths(workspacePaths, storagePaths)
		}

		// Glob expansion happens AFTER config validation ran on the
		// literal pattern, so the expanded matches must re-pass the
		// same infrastructure/reserved-suffix policy: `*` matches
		// dotfiles under filepath.Match semantics, and without this
		// filter `path: "*"` would sweep `.git`, `.jj`, or `.wrk.yml`
		// into management — planning a Move of repository metadata
		// into shared storage. Disallowed matches are SKIPPED (the
		// pattern means "manage everything manageable"), never
		// silently managed.
		filtered, err := filterDisallowed(root, workspacePaths)
		if err != nil {
			return nil, err
		}
		workspacePaths = filtered
	} else {
		workspacePaths = []string{
			filepath.Join(root, resource.Path),
		}
	}

	instances := make(
		[]ResourceInstance,
		0,
		len(workspacePaths),
	)

	for _, workspacePath := range workspacePaths {
		instance, err := newInstance(
			root,
			resource,
			workspacePath,
		)
		if err != nil {
			return nil, err
		}

		instances = append(
			instances,
			instance,
		)
	}

	return instances, nil
}

// filterDisallowed drops glob matches whose repository-relative path
// hits config.DisallowedResourcePath — repository infrastructure
// (.git, .jj, the wrk config files) and executor-reserved suffixes.
// Matches that cannot be made root-relative are also dropped; the
// per-instance containment check in newInstance is the authoritative
// escape guard, this is belt-and-braces for the skip path.
func filterDisallowed(root string, paths []string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if config.DisallowedResourcePath(filepath.Clean(rel)) != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// storageGlobMatches globs pattern against the shared-storage subtree
// and maps each match back to a workspace path under root. Bookkeeping
// siblings are dropped: `<name>.wrk-tmp` and friends are executor/gc
// scratch, never resources.
//
// KEEP IN SYNC (mentally) with engine.isBookkeeping — same suffix set,
// duplicated here because resolver must not import engine.
func storageGlobMatches(
	root, storageResourceRoot, pattern string,
) ([]string, error) {
	matches, err := filepath.Glob(
		filepath.Join(storageResourceRoot, pattern),
	)
	if err != nil {
		// The pattern already globbed clean against the workspace;
		// storage uses the same pattern, so this is unreachable in
		// practice — surface it rather than guess.
		return nil, err
	}

	absStorage, err := filepath.Abs(storageResourceRoot)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if isBookkeepingName(filepath.Base(match)) {
			continue
		}
		absMatch, err := filepath.Abs(match)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(absStorage, absMatch)
		if err != nil || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// A match outside the storage subtree can only come from
			// pattern trickery the workspace-side containment check
			// would also reject; skip it defensively.
			continue
		}
		paths = append(paths, filepath.Join(root, rel))
	}
	return paths, nil
}

// unionPaths merges two path slices, deduplicating on the cleaned path
// and returning a sorted result so downstream plan ordering is stable.
func unionPaths(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, p := range a {
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	for _, p := range b {
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

// bookkeepingSuffixes mirrors the executor's scratch/marker suffixes.
var bookkeepingSuffixes = [...]string{
	".wrk-lock",
	".wrk-tmp",
	".wrk-backup",
	".wrk-deleting",
	".wrk-forgetting",
	".wrk-provisioning",
}

func isBookkeepingName(name string) bool {
	for _, suffix := range bookkeepingSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func newInstance(
	root string,
	resource config.Resource,
	workspacePath string,
) (ResourceInstance, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ResourceInstance{}, err
	}
	absWs, err := filepath.Abs(workspacePath)
	if err != nil {
		return ResourceInstance{}, err
	}
	if absWs == absRoot {
		return ResourceInstance{}, fmt.Errorf(
			"resource %q resolves to the repository root; refusing",
			resource.Name,
		)
	}

	rel, err := filepath.Rel(absRoot, absWs)
	if err != nil {
		return ResourceInstance{}, err
	}
	if rel == "." || strings.HasPrefix(rel, "..") {
		return ResourceInstance{}, fmt.Errorf(
			"resource %q resolves outside the repository (%s)",
			resource.Name, workspacePath,
		)
	}

	instance := ResourceInstance{
		Resource:      resource,
		Root:          root,
		WorkspacePath: workspacePath,
		RelativePath:  filepath.ToSlash(rel),
	}

	// Fingerprint inputs are expanded with an empty shared path: a
	// fingerprint must never depend on the shared storage location.
	ctx := instance.Context("")

	fingerprintInputs := make(
		[]string,
		0,
		len(resource.Fingerprint),
	)

	for _, input := range resource.Fingerprint {
		expanded, err := placeholders.ExpandStrict(input, ctx)
		if err != nil {
			return ResourceInstance{}, fmt.Errorf(
				"resource %q: fingerprint input: %w",
				resource.Name, err,
			)
		}

		// Containment: after placeholder expansion, the input MUST resolve
		// inside the repository root. Otherwise `{root}/../secret` (or any
		// path escaping via `..`) could pin a fingerprint to files outside
		// the repo — silently, and with cache-key consequences.
		resolved := expanded
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(absRoot, resolved)
		}
		resolved = filepath.Clean(resolved)

		rel, err := filepath.Rel(absRoot, resolved)
		if err != nil || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ResourceInstance{}, fmt.Errorf(
				"resource %q: fingerprint input %q escapes repository root",
				resource.Name, expanded,
			)
		}

		fingerprintInputs = append(fingerprintInputs, expanded)
	}

	instance.FingerprintInputs = fingerprintInputs

	return instance, nil
}

func isGlob(path string) bool {
	return strings.ContainsAny(
		path,
		"*?[",
	)
}
