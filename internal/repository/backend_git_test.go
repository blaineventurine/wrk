package repository

import (
	"reflect"
	"testing"
)

// TestParseWorktreePorcelainFiltersBareAndPrunable pins D6: the
// porcelain parser must return only live worktrees. Bare-primary
// records (no working tree, sometimes no `worktree` line at all) and
// prunable records (broken link that would fail Detect if walked)
// were previously indistinguishable from live ones because we grepped
// for `worktree <path>` lines only.
func TestParseWorktreePorcelainFiltersBareAndPrunable(t *testing.T) {
	// Three-record output. Live first (standard record with HEAD +
	// branch), prunable second (has a worktree line but is tagged
	// broken), bare third (no worktree line — the shape emitted for
	// bare-primary repos).
	//
	// Format follows git-worktree(1) --porcelain: records separated
	// by blank lines, "key value" lines, key-only sentinels.
	sample := "worktree /path/to/live\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef01\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /path/to/prunable\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef02\n" +
		"prunable gitdir file points to non-existent location\n" +
		"\n" +
		"bare\n"

	got := parseWorktreePorcelain(sample)
	want := []string{"/path/to/live"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain returned %v, want %v", got, want)
	}
}

// TestParseWorktreePorcelainBareWithWorktreeLineFiltered guards the
// other bare-primary shape: some git versions emit
// `worktree <path>\nbare` for the bare record. The `bare` sentinel
// must drop the whole record even when a `worktree` line is present.
func TestParseWorktreePorcelainBareWithWorktreeLineFiltered(t *testing.T) {
	sample := "worktree /path/to/bare-primary.git\n" +
		"bare\n" +
		"\n" +
		"worktree /path/to/live\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef01\n" +
		"branch refs/heads/main\n"

	got := parseWorktreePorcelain(sample)
	want := []string{"/path/to/live"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain returned %v, want %v", got, want)
	}
}

// TestParseWorktreePorcelainBareOnly returns an empty slice — a bare
// primary with no worktrees is a legitimate output and must not
// synthesize a fake path.
func TestParseWorktreePorcelainBareOnly(t *testing.T) {
	sample := "worktree /path/to/bare.git\n" +
		"bare\n"

	got := parseWorktreePorcelain(sample)
	if len(got) != 0 {
		t.Fatalf("parseWorktreePorcelain returned %v, want empty", got)
	}
}

// TestParseWorktreePorcelainMultipleLive keeps the happy path honest:
// several live worktrees, all returned, in order.
func TestParseWorktreePorcelainMultipleLive(t *testing.T) {
	sample := "worktree /a\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef01\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /b\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef02\n" +
		"branch refs/heads/feature\n" +
		"\n" +
		"worktree /c\n" +
		"HEAD abcdef0123456789abcdef0123456789abcdef03\n" +
		"detached\n"

	got := parseWorktreePorcelain(sample)
	want := []string{"/a", "/b", "/c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain returned %v, want %v", got, want)
	}
}
