package resolver

import (
	"path/filepath"
	"sort"

	"github.com/blaineventurine/wrk/internal/config"
)

// ResolveGroup expands all paths belonging to one grouped resource.
func ResolveGroup(root, _ string, resource config.Resource) ([]ResourceInstance, error) {
	paths := make(map[string]bool)
	for _, pattern := range resource.Paths {
		if !isGlob(pattern) {
			paths[filepath.Join(root, pattern)] = true
			continue
		}
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			paths[match] = true
		}
		base := filepath.Base(pattern)
		if !isGlob(base) {
			parents, err := filepath.Glob(filepath.Join(root, filepath.Dir(pattern)))
			if err != nil {
				return nil, err
			}
			for _, parent := range parents {
				paths[filepath.Join(parent, base)] = true
			}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	filtered, err := filterDisallowed(root, ordered)
	if err != nil {
		return nil, err
	}
	instances := make([]ResourceInstance, 0, len(filtered))
	for _, path := range filtered {
		instance, err := newInstance(root, resource, path)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	if len(instances) > 0 {
		inputs := make(map[string]bool)
		for _, input := range instances[0].FingerprintInputs {
			if !isGlob(input) {
				inputs[input] = true
				continue
			}
			matches, err := filepath.Glob(input)
			if err != nil {
				return nil, err
			}
			if len(matches) == 0 {
				inputs[input] = true
			}
			for _, match := range matches {
				inputs[match] = true
			}
		}
		groupInputs := make([]string, 0, len(inputs))
		for input := range inputs {
			groupInputs = append(groupInputs, input)
		}
		sort.Strings(groupInputs)
		for i := range instances {
			instances[i].FingerprintInputs = groupInputs
		}
	}
	return instances, nil
}
