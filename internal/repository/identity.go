package repository

import (
	"crypto/sha256"
	"encoding/hex"
	urlpkg "net/url"
	"path/filepath"
	"strings"
)

// repositoryID derives a stable identifier shared across all workspaces of
// the same repository.
//
// It prefers the parsed "origin" remote (for example, github.com/org/repo)
// so that clones on different machines share storage. When no usable remote
// exists, it falls back to a hash of the repository's common metadata
// directory, namespaced under "local/".
func repositoryID(root, gitDir string) (string, error) {
	if url := originURL(root); url != "" {
		if id := parseRemote(url); id != "" {
			return id, nil
		}
	}

	hash := sha256.Sum256([]byte(gitDir))

	return filepath.Join(
		"local",
		hex.EncodeToString(hash[:])[:16],
	), nil
}

// originURL returns the URL of the "origin" remote, or "" if there is none.
func originURL(root string) string {
	out, err := capture(root, "git", "remote", "get-url", "origin")
	if err != nil {
		return ""
	}

	return strings.TrimSpace(out)
}

// parseRemote converts a Git remote URL into a host/path identifier, or ""
// if the URL is not recognized.
//
// Examples:
//
//	git@github.com:org/repo.git      -> github.com/org/repo
//	https://github.com/org/repo.git  -> github.com/org/repo
//	ssh://git@host:22/org/repo       -> host/org/repo
func parseRemote(url string) string {
	switch {
	case strings.HasPrefix(url, "git@"):
		// scp-like syntax: git@host:path
		parts := strings.SplitN(url[len("git@"):], ":", 2)
		if len(parts) != 2 {
			return ""
		}

		return filepath.ToSlash(
			filepath.Join(
				parts[0],
				strings.TrimSuffix(parts[1], ".git"),
			),
		)

	case strings.HasPrefix(url, "https://"),
		strings.HasPrefix(url, "http://"),
		strings.HasPrefix(url, "ssh://"),
		strings.HasPrefix(url, "git://"):

		u, err := urlpkg.Parse(url)
		if err != nil {
			return ""
		}

		path := strings.TrimPrefix(
			strings.TrimSuffix(u.Path, ".git"),
			"/",
		)

		return filepath.ToSlash(
			filepath.Join(
				u.Host,
				path,
			),
		)
	}

	return ""
}
