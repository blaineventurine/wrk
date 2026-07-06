package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"

	urlpkg "net/url"
)

func repositoryID(
	root string,
	gitDir string,
) (string, error) {
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

func originURL(root string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

func gitCommonDir(root string) (string, error) {
	jj := exec.Command("jj", "git", "root")
	jj.Dir = root

	if out, err := jj.Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	git := exec.Command("git", "rev-parse", "--git-common-dir")
	git.Dir = root

	out, err := git.Output()
	if err != nil {
		return "", err
	}

	path := strings.TrimSpace(string(out))

	if filepath.IsAbs(path) {
		return path, nil
	}

	return filepath.Join(root, path), nil
}

func parseRemote(url string) string {
	switch {
	case strings.HasPrefix(url, "git@"):
		parts := strings.SplitN(url[4:], ":", 2)
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
