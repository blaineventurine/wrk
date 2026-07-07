package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads the wrk configuration from the repository root.
//
// It always requires .wrk.yml. If .wrk.local.yml also exists, its resources
// are merged in: entries with the same Name as a shared resource replace it
// entirely (marked OriginLocalOverride); entries with new names are
// appended (marked OriginLocal).
//
// After merging, every resource is validated. A single invalid resource
// aborts the load — wrk will not operate on a partially-valid config.
func Load(root string) (*Config, error) {
	shared, err := loadFile(filepath.Join(root, Filename), true)
	if err != nil {
		return nil, err
	}
	tag(shared.Resources, OriginShared)

	local, err := loadFile(filepath.Join(root, LocalFilename), false)
	if err != nil {
		return nil, err
	}
	if local != nil {
		tag(local.Resources, OriginLocal)
		shared.Resources = merge(shared.Resources, local.Resources)
	}

	if err := validate(shared); err != nil {
		return nil, err
	}

	return shared, nil
}

// validate rejects configurations that could cause wrk to operate on
// paths outside the repository or on repository infrastructure.
//
// Every resource must have:
//   - a non-empty Name (used for identification everywhere in wrk)
//   - a non-empty Path that is repository-relative
//   - a Path that resolves inside the repository (no "..", no leading "/")
//   - a Path that is not the repository root itself
//   - a Path that does not point at repository infrastructure (.git, .jj,
//     .wrk.yml, .wrk.local.yml)
//
// Names must also be unique across the merged configuration.
func validate(cfg *Config) error {
	seen := make(map[string]bool, len(cfg.Resources))

	// Paths whose management would be catastrophic or nonsensical.
	forbidden := map[string]bool{
		".git":        true,
		".jj":         true,
		Filename:      true,
		LocalFilename: true,
	}

	for i, r := range cfg.Resources {
		context := fmt.Sprintf("resource %d", i)
		if r.Name != "" {
			context = fmt.Sprintf("resource %q", r.Name)
		}

		if r.Name == "" {
			return fmt.Errorf(
				"%s: name is required (every resource must have a unique name)",
				context,
			)
		}

		if seen[r.Name] {
			return fmt.Errorf(
				"%s: duplicate name (each resource must have a unique name)",
				context,
			)
		}
		seen[r.Name] = true

		if r.Path == "" {
			return fmt.Errorf("%s: path is required", context)
		}

		clean := filepath.Clean(r.Path)

		if filepath.IsAbs(clean) {
			return fmt.Errorf(
				"%s: path %q must be repository-relative, not absolute",
				context, r.Path,
			)
		}

		if clean == "." {
			return fmt.Errorf(
				"%s: path %q refers to the repository root; wrk will not "+
					"manage the repository itself",
				context, r.Path,
			)
		}

		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf(
				"%s: path %q escapes the repository root",
				context, r.Path,
			)
		}

		if forbidden[clean] {
			return fmt.Errorf(
				"%s: path %q would manage repository infrastructure",
				context, r.Path,
			)
		}

		// Also reject anything inside a forbidden top-level directory
		// (e.g. ".git/hooks", ".jj/repo").
		firstSegment := clean
		if sep := strings.Index(clean, string(filepath.Separator)); sep >= 0 {
			firstSegment = clean[:sep]
		}
		if forbidden[firstSegment] {
			return fmt.Errorf(
				"%s: path %q is inside repository infrastructure (%s)",
				context, r.Path, firstSegment,
			)
		}
	}

	return nil
}

// loadFile reads and parses one config file.
//
// If required is false and the file does not exist, it returns (nil, nil).
func loadFile(path string, required bool) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !required {
				return nil, nil
			}
			return nil, fmt.Errorf("%w: %s", ErrConfigNotFound, path)
		}
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrConfigIsDirectory, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	normalize(cfg)
	return cfg, nil
}

// tag sets Origin on every resource in the slice.
func tag(resources []Resource, origin Origin) {
	for i := range resources {
		resources[i].Origin = origin
	}
}

// merge combines shared and local resources. Local entries with the same
// Name as a shared entry replace the shared entry (in place); local
// entries with unmatched names are appended.
//
// Local entries without a Name are always treated as additions.
func merge(shared, local []Resource) []Resource {
	byName := map[string]int{}
	for i, r := range local {
		if r.Name == "" {
			continue
		}
		byName[r.Name] = i
	}

	consumed := map[int]bool{}
	result := make([]Resource, 0, len(shared)+len(local))

	for _, s := range shared {
		if s.Name != "" {
			if i, ok := byName[s.Name]; ok {
				override := local[i]
				override.Origin = OriginLocalOverride
				result = append(result, override)
				consumed[i] = true
				continue
			}
		}
		result = append(result, s)
	}

	for i, l := range local {
		if consumed[i] {
			continue
		}
		result = append(result, l)
	}

	return result
}

func normalize(cfg *Config) {
	if cfg.Resources == nil {
		cfg.Resources = []Resource{}
	}

	for i := range cfg.Resources {
		resource := &cfg.Resources[i]

		if resource.Fingerprint == nil {
			resource.Fingerprint = []string{}
		}

		if resource.Hooks == nil {
			resource.Hooks = make(map[string][]Command)
		}
	}
}
