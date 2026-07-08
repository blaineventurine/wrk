package repository

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDestinationBareNameBecomesSibling(t *testing.T) {
	got := resolveDestination("/proj/main", "feature")
	want := filepath.Clean("/proj/feature")
	if got != want {
		t.Fatalf("resolveDestination(%q, %q) = %q, want %q",
			"/proj/main", "feature", got, want)
	}
}

func TestResolveDestinationAbsolutePathIsRespected(t *testing.T) {
	got := resolveDestination("/proj/main", "/somewhere/else")
	if got != filepath.Clean("/somewhere/else") {
		t.Fatalf("absolute path should be untouched, got %q", got)
	}
}

func TestResolveDestinationExplicitRelativeStaysRelativeToRoot(t *testing.T) {
	// A path with a separator is treated literally against root so
	// long-time users of `wrk new ../feature` keep the same behaviour.
	cases := map[string]string{
		"../feature":  "/proj/feature",
		"./sub/thing": "/proj/main/sub/thing",
		"sub/thing":   "/proj/main/sub/thing",
	}
	for input, want := range cases {
		got := resolveDestination("/proj/main", input)
		if got != filepath.Clean(want) {
			t.Errorf("resolveDestination(%q, %q) = %q, want %q",
				"/proj/main", input, got, filepath.Clean(want))
		}
	}
}

func TestResolveDestinationDotAndDotDotAreLiteral(t *testing.T) {
	// "." and ".." are not bare names — they mean the current or
	// parent directory literally, and should not become "../." or
	// "../..".
	if got := resolveDestination("/proj/main", "."); got != filepath.Clean("/proj/main") {
		t.Errorf(`resolveDestination(root, ".") = %q, want root itself`, got)
	}
	if got := resolveDestination("/proj/main", ".."); got != filepath.Clean("/proj") {
		t.Errorf(`resolveDestination(root, "..") = %q, want parent`, got)
	}
}

func TestContainingWorkspaceDetectsEqualAndNested(t *testing.T) {
	workspaces := []string{"/proj/main", "/proj/feature"}

	cases := []struct {
		name string
		dest string
		want string
	}{
		{"equal to workspace", "/proj/main", "/proj/main"},
		{"nested inside workspace", "/proj/main/nested", "/proj/main"},
		{"deeply nested", "/proj/feature/a/b/c", "/proj/feature"},
		{"sibling is fine", "/proj/other", ""},
		{"parent of workspaces is fine", "/proj", ""},
		{"unrelated tree is fine", "/elsewhere/foo", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := containingWorkspace(tc.dest, workspaces)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("containingWorkspace(%q) = %q, want %q",
					tc.dest, got, tc.want)
			}
		})
	}
}

func TestContainingWorkspacePrefixMatchIsNotSubdirectory(t *testing.T) {
	// /proj/main-2 is NOT inside /proj/main, even though the string
	// starts with the same prefix. The path-component check must
	// catch this — a naive strings.HasPrefix would falsely match.
	workspaces := []string{"/proj/main"}
	got, err := containingWorkspace("/proj/main-2", workspaces)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("prefix-only match should not be inside; got %q", got)
	}
}

// TestResolveDestinationRejectsEmpty pins S8: an empty destination
// used to fall through resolveDestination, collapse to r.Root, and
// then hit the "destination already exists" branch — a confusing
// error for what is really a "you didn't name anything" mistake. The
// upfront check now rejects it cleanly with a message that quotes the
// bad input.
func TestResolveDestinationRejectsEmpty(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination("")
	if err == nil {
		t.Fatal("ResolveDestination(\"\") should error")
	}
	if !strings.Contains(err.Error(), "destination cannot be") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveDestinationRejectsDot pins S8: "." is not a workspace
// destination — it means "the current directory", which is the
// existing workspace. Reject it up front instead of letting the
// "already exists" check catch it downstream.
func TestResolveDestinationRejectsDot(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination(".")
	if err == nil {
		t.Fatal(`ResolveDestination(".") should error`)
	}
	if !strings.Contains(err.Error(), `"."`) {
		t.Fatalf("error should quote the bad destination, got: %v", err)
	}
}

// TestResolveDestinationRejectsDotDot pins S8: ".." would land on the
// parent directory, which is almost certainly not what the user
// wanted. Reject with the same message shape as "".
func TestResolveDestinationRejectsDotDot(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination("..")
	if err == nil {
		t.Fatal(`ResolveDestination("..") should error`)
	}
	if !strings.Contains(err.Error(), `".."`) {
		t.Fatalf("error should quote the bad destination, got: %v", err)
	}
}

// TestResolveDestinationRejectsWhitespace pins S8: `wrk new "  "`
// should fail cleanly. strings.TrimSpace makes whitespace-only
// destinations equivalent to the empty case, so the user does not
// wander into a "path exists at /proj/main/  " error.
func TestResolveDestinationRejectsWhitespace(t *testing.T) {
	r := &Repository{Root: "/proj/main"}
	_, err := r.ResolveDestination("  ")
	if err == nil {
		t.Fatal(`ResolveDestination("  ") should error`)
	}
	if !strings.Contains(err.Error(), "destination cannot be") {
		t.Fatalf("unexpected error: %v", err)
	}
}
