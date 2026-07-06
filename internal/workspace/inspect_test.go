package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectSymlinkCapturesLinkText(t *testing.T) {
	dir := t.TempDir()

	// A real shared target so EvalSymlinks resolves.
	shared := filepath.Join(dir, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}

	state, err := Inspect(link, shared)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	if !state.WorkspaceSymlink {
		t.Fatalf("expected WorkspaceSymlink true")
	}

	// Link text is exactly what we wrote.
	if state.WorkspaceLinkText != shared {
		t.Errorf("WorkspaceLinkText = %q, want %q", state.WorkspaceLinkText, shared)
	}

	if !state.SharedExists {
		t.Errorf("expected SharedExists true")
	}
}

func TestInspectDanglingSymlink(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "does-not-exist")
	link := filepath.Join(dir, "link")

	if err := os.Symlink(missing, link); err != nil {
		t.Fatal(err)
	}

	state, err := Inspect(link, missing)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	if !state.WorkspaceSymlink {
		t.Fatalf("expected WorkspaceSymlink true")
	}

	// Link text is still captured even though the target is missing...
	if state.WorkspaceLinkText != missing {
		t.Errorf("WorkspaceLinkText = %q, want %q", state.WorkspaceLinkText, missing)
	}

	// ...but the resolved target is empty because EvalSymlinks fails.
	if state.WorkspaceTarget != "" {
		t.Errorf("WorkspaceTarget = %q, want empty", state.WorkspaceTarget)
	}
}

func TestInspectMissingWorkspace(t *testing.T) {
	root := t.TempDir()

	state, err := Inspect(
		filepath.Join(root, "node_modules"),
		filepath.Join(root, "shared"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if state.WorkspaceExists {
		t.Fatal("expected workspace to be missing")
	}

	if state.SharedExists {
		t.Fatal("expected shared to be missing")
	}
}

func TestInspectWorkspaceDirectory(t *testing.T) {
	root := t.TempDir()

	workspace := filepath.Join(root, "node_modules")

	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	state, err := Inspect(
		workspace,
		filepath.Join(root, "shared"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if !state.WorkspaceExists {
		t.Fatal("expected workspace to exist")
	}

	if state.WorkspaceSymlink {
		t.Fatal("expected workspace not to be a symlink")
	}
}

func TestInspectSymlink(t *testing.T) {
	root := t.TempDir()

	target := filepath.Join(root, "shared")

	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "node_modules")

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	state, err := Inspect(link, target)
	if err != nil {
		t.Fatal(err)
	}

	if !state.WorkspaceSymlink {
		t.Fatal("expected symlink")
	}

	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}

	if state.WorkspaceTarget != resolvedTarget {
		t.Fatalf(
			"got  %q\nwant %q",
			state.WorkspaceTarget,
			resolvedTarget,
		)
	}

	if !state.SharedExists {
		t.Fatal("expected shared to exist")
	}
}
