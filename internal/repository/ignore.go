package repository

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Prepare configures the local repository for wrk.
//
// This modifies only repository-local configuration. It never changes
// tracked files such as .gitignore.
func (r *Repository) Prepare(
	paths ...string,
) error {
	return r.ensureIgnored(paths)
}

func (r *Repository) ensureIgnored(paths []string) error {
	exclude := filepath.Join(
		r.metadataDir,
		"info",
		"exclude",
	)

	if err := os.MkdirAll(
		filepath.Dir(exclude),
		0o755,
	); err != nil {
		return err
	}

	existing, err := readPatterns(exclude)
	if err != nil {
		return err
	}

	var additions []string

	for _, path := range paths {
		pattern := filepath.ToSlash(path)

		// Already ignored exactly.
		if existing[pattern] {
			continue
		}

		// Warn (don't silently fix) if a directory-only rule exists.
		if existing[pattern+"/"] {
			return fmt.Errorf(
				"%q is ignored only as a directory. "+
					"wrk materializes managed directories as symlinks. "+
					"Please ignore %q instead",
				pattern+"/",
				pattern,
			)
		}

		additions = append(
			additions,
			pattern,
		)
	}

	if len(additions) == 0 {
		return nil
	}

	file, err := os.OpenFile(
		exclude,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	if info.Size() > 0 {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}

	if _, err := file.WriteString(
		"# Added by wrk\n",
	); err != nil {
		return err
	}

	for _, pattern := range additions {
		if _, err := file.WriteString(
			pattern + "\n",
		); err != nil {
			return err
		}
	}

	return nil
}

func readPatterns(path string) (map[string]bool, error) {
	patterns := make(map[string]bool)

	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return patterns, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(
			scanner.Text(),
		)

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		patterns[line] = true
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return patterns, nil
}
