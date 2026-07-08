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
// It prefers a parsed remote URL (for example, github.com/org/repo) so
// that clones on different machines share storage. When no usable
// remote exists, it falls back to a hash of the repository's common
// metadata directory, namespaced under "local/".
func repositoryID(root, gitDir string) (string, error) {
	if url := preferredRemoteURL(root); url != "" {
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

// preferredRemoteURL returns the URL of the remote wrk considers
// canonical for identity purposes, or "" if none is usable.
//
// Preference order:
//
//  1. "origin" — the overwhelming common case.
//  2. "upstream" — fork-first workflows where the user's own fork is
//     tracked under a different name (or no remote at all) and
//     "upstream" points at the shared repository.
//  3. The sole remote, if exactly one is configured under some other
//     name — captures ad-hoc names ("gh", "gl", ...) where the choice
//     is unambiguous.
//
// When multiple non-{origin,upstream} remotes are configured we
// deliberately give up: picking the "wrong" one would move a
// repository's workspace storage under a different identity on the
// next detection, which is more disruptive than falling back to the
// local hash.
func preferredRemoteURL(root string) string {
	for _, name := range []string{"origin", "upstream"} {
		if url := remoteURL(root, name); url != "" {
			return url
		}
	}

	out, err := capture(root, "git", "remote")
	if err != nil {
		return ""
	}
	names := strings.Fields(strings.TrimSpace(out))
	if len(names) == 1 {
		return remoteURL(root, names[0])
	}
	return ""
}

// remoteURL returns the fetch URL of the named remote, or "" if the
// remote is missing or git rejected the query.
func remoteURL(root, name string) string {
	out, err := capture(root, "git", "remote", "get-url", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// parseRemote converts a Git remote URL into a host/path identifier, or ""
// if the URL is not recognized.
//
// The host portion is lowercased and the scheme's default port is stripped
// (`:22` for ssh/git, `:80` for http, `:443` for https) so that
// `https://GitHub.com/…`, `https://github.com:443/…`, and
// `ssh://git@github.com:22/…` all converge on `github.com/…`. The path is
// preserved verbatim — repository paths are case-sensitive on the wire and
// forge servers reject folded variants.
//
// Host aliases (e.g. `ssh.github.com` vs `github.com`, or user-configured
// SSH `Host` shortcuts) are NOT resolved: users who want those to share
// storage must standardize their `origin` remote.
//
// Examples:
//
//	git@github.com:org/repo.git         -> github.com/org/repo
//	https://github.com/org/repo.git     -> github.com/org/repo
//	https://GitHub.com/Org/Repo.git     -> github.com/Org/Repo
//	ssh://git@github.com:22/org/repo    -> github.com/org/repo
//	ssh://git@host:2222/org/repo        -> host:2222/org/repo
func parseRemote(url string) string {
	switch {
	case strings.HasPrefix(url, "git@"):
		// scp-like syntax: git@host:path. First colon is the separator.
		host, path, ok := strings.Cut(url[len("git@"):], ":")
		if !ok || host == "" || path == "" {
			return ""
		}

		return filepath.ToSlash(
			filepath.Join(
				strings.ToLower(host),
				strings.TrimSuffix(path, ".git"),
			),
		)

	case strings.HasPrefix(url, "https://"),
		strings.HasPrefix(url, "http://"),
		strings.HasPrefix(url, "ssh://"),
		strings.HasPrefix(url, "git://"):

		u, err := urlpkg.Parse(url)
		if err != nil || u.Host == "" {
			return ""
		}

		host := normalizeHost(u.Scheme, u.Host)

		path := strings.TrimPrefix(
			strings.TrimSuffix(u.Path, ".git"),
			"/",
		)
		if path == "" {
			return ""
		}

		return filepath.ToSlash(
			filepath.Join(
				host,
				path,
			),
		)
	}

	return ""
}

// normalizeHost lowercases the host and strips the scheme's default port
// so that the same remote written with an explicit default port matches
// the elided form. Non-default ports are preserved (`example.com:2222`).
func normalizeHost(scheme, host string) string {
	host = strings.ToLower(host)

	name, port, ok := strings.Cut(host, ":")
	if !ok {
		return host
	}

	if port == defaultPort(scheme) {
		return name
	}

	return host
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	case "ssh", "git":
		return "22"
	}
	return ""
}
