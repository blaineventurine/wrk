package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

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
