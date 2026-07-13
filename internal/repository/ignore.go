package repository

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// wrkHeader opens the block of patterns wrk manages in a repository's
// exclude file. The literal is unchanged from earlier wrk releases so
// files written by them are recognized and upgraded in place.
const wrkHeader = "# Added by wrk"

// wrkFooter closes the wrk-managed block. Everything between header and
// footer is rewritten on every Prepare to exactly the currently-needed
// pattern set — patterns for resources that left the config are pruned
// instead of accreting forever. User rules outside the block are never
// touched.
const wrkFooter = "# End of wrk-managed block (do not edit between markers)"

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

// ensureIgnored rewrites the wrk-managed block of the repository-local
// exclude file so it contains exactly the patterns wrk currently needs.
//
// Layout invariants:
//
//   - user content before the header and after the footer is preserved
//     byte-for-byte;
//   - the managed block is rebuilt from `paths` on every call, so
//     removing a resource from .wrk.yml prunes its pattern on the next
//     Prepare;
//   - a pattern the user already ignores outside the block is not
//     duplicated inside it;
//   - a legacy block (header with no footer, written by earlier wrk
//     versions) is upgraded: the contiguous non-blank, non-comment lines
//     after the header are adopted as the managed block and rewritten.
//
// A directory-only rule (`pattern/`) that would shadow a wrk pattern is
// surfaced as a warning on stderr, and the slash-less pattern is added
// alongside it: gitignore rules coexist, and the slash-less form is the
// one that covers wrk's symlinks. Earlier versions hard-failed here,
// which blocked `wrk link` until the user hand-edited their exclude
// file.
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

	doc := parseExcludeDoc(original)

	outside := patternSet(doc.before, doc.after)

	seen := make(map[string]bool, len(paths))
	managed := make([]string, 0, len(paths))
	for _, path := range paths {
		pattern := filepath.ToSlash(path)

		if seen[pattern] {
			continue
		}
		seen[pattern] = true

		// The user already ignores it with their own rule; leave
		// their spelling authoritative and keep the block minimal.
		if outside[pattern] {
			continue
		}

		// A directory-only rule (`pattern/`) does not ignore the
		// symlink wrk materializes at `pattern`. Add the slash-less
		// form alongside — both rules coexist harmlessly — and tell
		// the operator why the block gained a near-duplicate.
		if collision, ok := directoryOnlyCollision(outside, pattern); ok {
			fmt.Fprintf(os.Stderr,
				"wrk: exclude rule %q in %s is directory-only; "+
					"adding %q alongside so the wrk-managed symlink is ignored too\n",
				collision, exclude, pattern,
			)
		}

		managed = append(managed, pattern)
	}

	next := renderExcludeDoc(doc, managed)
	if bytes.Equal(next, original) {
		return nil
	}

	return atomicWriteFile(exclude, next, 0o644)
}

// excludeDoc is a parsed exclude file: verbatim user content around the
// wrk-managed block. The block's previous contents are deliberately NOT
// carried — the whole point of the managed block is that wrk rebuilds
// it from the current config on every write.
type excludeDoc struct {
	// before holds the lines preceding the wrk header (or the entire
	// file when no header exists), verbatim.
	before []string
	// after holds the lines following the block, verbatim.
	after []string
}

// parseExcludeDoc splits data into user-owned regions around the
// wrk-managed block.
//
// Recognized block shapes:
//
//	header ... footer   — modern delimited block; interior discarded.
//	header ...          — legacy block (pre-footer wrk versions): the
//	                      contiguous run of non-blank, non-comment lines
//	                      after the header is treated as wrk-owned; the
//	                      first blank or comment line ends it.
//
// Only the FIRST header line is treated as a marker; any later
// duplicates are user content and survive verbatim.
func parseExcludeDoc(data []byte) excludeDoc {
	if len(data) == 0 {
		return excludeDoc{}
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")

	headerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == wrkHeader {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		return excludeDoc{before: lines}
	}

	doc := excludeDoc{before: lines[:headerIdx]}

	// Modern shape: explicit footer.
	for i := headerIdx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == wrkFooter {
			doc.after = lines[i+1:]
			return doc
		}
	}

	// Legacy shape: adopt the contiguous pattern run after the header.
	end := headerIdx + 1
	for end < len(lines) {
		trimmed := strings.TrimSpace(lines[end])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			break
		}
		end++
	}
	doc.after = lines[end:]
	return doc
}

// renderExcludeDoc reassembles the exclude file: user prefix, the
// managed block (omitted entirely when managed is empty), user suffix.
// Single blank lines separate the block from non-blank neighbours so
// repeated renders are byte-stable (the separator blanks re-parse into
// before/after and are not re-added).
func renderExcludeDoc(doc excludeDoc, managed []string) []byte {
	out := make([]string, 0,
		len(doc.before)+len(doc.after)+len(managed)+4)
	out = append(out, doc.before...)

	if len(managed) > 0 {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, wrkHeader)
		out = append(out, managed...)
		out = append(out, wrkFooter)
		if len(doc.after) > 0 && strings.TrimSpace(doc.after[0]) != "" {
			out = append(out, "")
		}
	}

	out = append(out, doc.after...)

	// Drop a trailing run of blank lines so block removal doesn't leave
	// a growing tail, then terminate the file with exactly one newline.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// patternSet collects the non-blank, non-comment lines of the given
// line slices — the user-owned exclude rules wrk must not duplicate.
func patternSet(lineGroups ...[]string) map[string]bool {
	patterns := make(map[string]bool)
	for _, lines := range lineGroups {
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			patterns[trimmed] = true
		}
	}
	return patterns
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
