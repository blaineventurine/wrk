package repository

import (
	"crypto/sha256"
	"encoding/hex"
	urlpkg "net/url"
	"path/filepath"
	"strings"
)

// repositoryID derives a stable identifier shared across all workspaces
// of the same repository. Prefers a parsed remote URL (e.g.
// github.com/org/repo) so clones on different machines share storage;
// falls back to a hash of the common git dir under "local/".
func repositoryID(root, gitDir string) (string, error) {
	if url := preferredRemoteURL(root, gitDir); url != "" {
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

// preferredRemoteURL returns the remote wrk considers canonical for
// identity, or "" if none is usable.
//
// Order: origin, upstream, or the sole configured remote. Multiple
// non-{origin,upstream} remotes are ambiguous — falling back to the
// local hash is safer than picking wrong.
func preferredRemoteURL(root, gitDir string) string {
	for _, name := range []string{"origin", "upstream"} {
		if url := remoteURL(root, gitDir, name); url != "" {
			return url
		}
	}

	out, err := capture(root, "git", "--git-dir", gitDir, "remote")
	if err != nil {
		return ""
	}
	names := strings.Fields(strings.TrimSpace(out))
	if len(names) == 1 {
		return remoteURL(root, gitDir, names[0])
	}
	return ""
}

// remoteURL returns the fetch URL of the named remote, or "" if the
// remote is missing or git rejected the query.
func remoteURL(root, gitDir, name string) string {
	out, err := capture(
		root, "git", "--git-dir", gitDir, "remote", "get-url", name,
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// parseRemote converts a Git remote URL into a host/path identifier,
// or "" if unrecognized.
//
// Host is lowercased and the scheme's default port stripped so
// https/http/ssh variants of the same repo converge. Path is verbatim
// (repo paths are case-sensitive on the wire). Host aliases (SSH
// `Host` shortcuts, ssh.github.com) are NOT resolved.
//
// Examples:
//
//	git@github.com:org/repo.git         -> github.com/org/repo
//	https://GitHub.com/Org/Repo.git     -> github.com/Org/Repo
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

	// Unknown URL scheme (e.g. file://, plain local paths, custom
	// remote helpers). Fall back to the local-hash identity path in
	// repositoryID rather than logging: the fallback is silent and
	// deterministic, and users hitting it usually already know their
	// remote is unusual.
	return ""
}

// normalizeHost lowercases the host and strips the scheme's default
// port so explicit and elided default ports match.
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
