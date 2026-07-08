package repository

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// wrkHeader marks the block of patterns wrk has added to a repository's
// exclude file. Written at most once per file: subsequent Prepare calls
// look for an existing line and skip re-emitting it.
const wrkHeader = "# Added by wrk"

// ensureIgnoredMu serializes in-process ensureIgnored calls. The
// atomic tmp+rename write below prevents readers from observing a
// partially-written exclude file, but two goroutines racing on the
// read/modify/write cycle would still lose updates. This mutex closes
// that gap; cross-process races still rely on rename atomicity.
var ensureIgnoredMu sync.Mutex

// Prepare configures the local repository for wrk.
//
// This modifies only repository-local configuration. It never changes
// tracked files such as .gitignore.
func (r *Repository) Prepare(paths ...string) error {
	return r.ensureIgnored(paths)
}

func (r *Repository) ensureIgnored(paths []string) error {
	ensureIgnoredMu.Lock()
	defer ensureIgnoredMu.Unlock()

	exclude := filepath.Join(r.metadataDir, "info", "exclude")

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}

	original, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	existing, err := readPatterns(bytes.NewReader(original), exclude)
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
		if collision, ok := directoryOnlyCollision(
			existing,
			pattern,
		); ok {
			return fmt.Errorf(
				"exclude rule %q in %s is directory-only; "+
					"wrk materializes managed directories as symlinks, "+
					"so add %q (without trailing slash) alongside or in "+
					"place of it",
				collision,
				exclude,
				pattern,
			)
		}

		additions = append(additions, pattern)
	}

	if len(additions) == 0 {
		return nil
	}

	buf := make(
		[]byte,
		0,
		len(original)+len(wrkHeader)+2+64*len(additions),
	)
	buf = append(buf, original...)
	if len(buf) > 0 && buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}
	if !hasWrkHeader(original) {
		if len(buf) > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, wrkHeader...)
		buf = append(buf, '\n')
	}
	for _, pattern := range additions {
		buf = append(buf, pattern...)
		buf = append(buf, '\n')
	}

	return atomicWriteFile(exclude, buf, 0o644)
}

// atomicWriteFile writes data to path via a same-directory temp file
// and rename. Rename is atomic on POSIX, so readers see either the
// pre-write or post-write content, never a partial file.
func atomicWriteFile(
	path string,
	data []byte,
	mode os.FileMode,
) error {
	tmp, err := os.CreateTemp(
		filepath.Dir(path),
		filepath.Base(path)+".*.tmp",
	)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// hasWrkHeader reports whether data already contains a line whose
// trimmed content is exactly wrkHeader.
func hasWrkHeader(data []byte) bool {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if string(bytes.TrimSpace(line)) == wrkHeader {
			return true
		}
	}
	return false
}

// directoryOnlyCollision reports whether existing contains a
// directory-only rule that would shadow pattern under any of the
// common gitignore prefix variants. The exact "pattern/" form is
// checked first so it wins the returned message when several forms are
// present.
func directoryOnlyCollision(
	existing map[string]bool,
	pattern string,
) (string, bool) {
	forms := [...]string{
		pattern + "/",
		"./" + pattern + "/",
		"/" + pattern + "/",
		"**/" + pattern + "/",
	}
	for _, f := range forms {
		if existing[f] {
			return f, true
		}
	}
	return "", false
}

// readPatterns parses exclude-file rules from r, skipping blank lines
// and comments. path is used only for error context.
func readPatterns(
	r io.Reader,
	path string,
) (map[string]bool, error) {
	patterns := make(map[string]bool)

	scanner := bufio.NewScanner(r)
	// Allow single rules up to 1 MiB. The default 64 KiB limit rejects
	// pathological but legal files with `bufio.ErrTooLong`.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		patterns[line] = true
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return patterns, nil
}
