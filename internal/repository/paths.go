package repository

import "path/filepath"

// canonicalize returns the symlink-resolved form of path, walking up
// to the deepest existing ancestor when path itself does not yet
// exist (as is the case for a to-be-created workspace destination).
// If no ancestor resolves — e.g. path is entirely fictional — the
// input is returned unchanged.
//
// This matters on macOS, where /var, /tmp and /var/folders/... are
// symlinks into /private, and every downstream path comparison in
// this package (workspace nesting, current-workspace matching, …)
// assumes both sides are canonical.
func canonicalize(path string) string {
	var suffix []string
	current := path
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(append([]string{resolved}, suffix...)...)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}
