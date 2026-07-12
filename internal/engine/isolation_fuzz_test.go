package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoadIsolationTolerant pins loadIsolation's corruption-tolerance
// contract. Users can (and do) reach into `<metadata>/wrk/isolated.json`
// with a text editor; a truncated write from a crashed sibling process,
// a partial rsync, or an over-eager backup tool can leave the file in
// any state at all. loadIsolation MUST NOT panic, MUST always yield a
// non-nil registry the caller can index into, and MUST NOT surface a
// JSON parse error as a bubble-up (parse failures are logged to stderr
// and the registry is treated as empty — matching detachRegistry's
// tolerance discipline). Only a real I/O error is bubbled up, and the
// fuzz harness prepares the parent dir + file eagerly so I/O errors
// are not the path under test.
//
// Invariants asserted per input:
//
//  1. loadIsolation does not panic on any byte sequence.
//  2. It returns (non-nil, nil) — even for total garbage. A nil map
//     would NPE the very-next `reg[workspaceRoot]` access downstream
//     (recordIsolation, isIsolated), so the "always empty on corrupt"
//     contract has to include non-nil-ness.
//
// Seeds sweep the categories most likely to reveal a parse or type-
// assertion bug: empty, valid empty, well-typed content, malformed
// JSON, wrong outer type (array/string/number), and raw bytes.
//
// A literal-`null` payload used to decode with nil error but leave
// the map nil (json.Unmarshal treats a bare `null` as "reset the
// value"), which then NPE'd the next recordIsolation. loadIsolation
// now coerces that shape back to an empty non-nil registry, so
// `null` is seeded like every other tolerated corruption — the fuzz
// invariants (no panic, non-nil registry, no bubbled error) must
// hold for it too.
func FuzzLoadIsolationTolerant(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		[]byte(`{}`),
		[]byte(`{"/repo": {"node_modules": {"storagePath": "/s/x", "createdAt": "2026-01-01T00:00:00Z"}}}`),
		[]byte(`not json {`),
		[]byte(`{"broken":`),
		[]byte(`[]`),
		[]byte(`"just a string"`),
		[]byte(`12345`),
		{0, 1, 2, 3, 4},
		[]byte(`null`),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		repo := newTestRepoWithHead(t, map[string]string{".wrk.yml": "resources: []\n"})

		path := isolationPath(repo)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			// A mkdir failure here is a fuzz-harness setup issue, not
			// a property violation. Skip so the fuzzer keeps drawing.
			t.Skipf("mkdir parent of %s: %v (not a fuzz failure)", path, err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Skipf("write fuzz input to %s: %v (not a fuzz failure)", path, err)
		}

		reg, err := loadIsolation(repo)
		if err != nil {
			t.Fatalf("loadIsolation returned error for tolerated corruption (len=%d): %v",
				len(data), err)
		}
		if reg == nil {
			t.Fatal("loadIsolation returned nil registry; contract is non-nil empty on corrupt input")
		}
	})
}
