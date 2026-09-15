package engine

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

func TestLinkInitializesGroupedResourceOnce(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)
	if err := os.MkdirAll(filepath.Join(repo.Root, "packages", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo.Root, "lock"), "v1")
	writeConfig(t, repo.Root, config.Filename, "resources:\n"+
		"  - name: node\n"+
		"    paths: [node_modules, packages/*/node_modules]\n"+
		"    fingerprint: [\"{root}/lock\"]\n"+
		"    hooks:\n"+
		"      initialize:\n"+
		"        - run: sh -c 'echo x >> hook-count; touch node_modules/root packages/app/node_modules/app'\n"+
		"          cwd: \"{root}\"\n")

	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	count, err := os.ReadFile(filepath.Join(repo.Root, "hook-count"))
	if err != nil || string(count) != "x\n" {
		t.Fatalf("hook count = %q, err=%v", count, err)
	}
	for _, path := range []string{"node_modules", "packages/app/node_modules"} {
		info, err := os.Lstat(filepath.Join(repo.Root, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s not linked: mode=%v", path, info.Mode())
		}
	}
	report, err := Status(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(report.Rows) != 2 || report.Rows[0].State != StateLinked || report.Rows[1].State != StateLinked {
		t.Fatalf("status rows = %+v", report.Rows)
	}
}

func TestGroupedNodeModulesPreservesNodeResolutionAncestry(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, "lock"), "v1")
	writeConfig(t, repo.Root, config.Filename, "resources:\n"+
		"  - name: node\n"+
		"    paths: [_client/node_modules]\n"+
		"    fingerprint: [\"{root}/lock\"]\n"+
		"    hooks:\n"+
		"      initialize:\n"+
		"        - run: mkdir -p {shared}\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	modules := filepath.Join(repo.Root, "_client", "node_modules")
	writeFile(t, filepath.Join(modules, "a", "package.json"), `{"name":"a","type":"module","exports":"./index.js"}`)
	writeFile(t, filepath.Join(modules, "a", "index.js"), `import value from "b"; console.log(value)`)
	writeFile(t, filepath.Join(modules, "b", "package.json"), `{"name":"b","type":"module","exports":"./index.js"}`)
	writeFile(t, filepath.Join(modules, "b", "index.js"), `export default "resolved"`)
	cmd := exec.Command(node, filepath.Join(modules, "a", "index.js"))
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "resolved\n" {
		t.Fatalf("node output=%q err=%v", out, err)
	}
}

func TestGroupedResourceRefusesUserSymlink(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, "lock"), "v1")
	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: node\n    paths: [node_modules]\n    fingerprint: [\"{root}/lock\"]\n    hooks:\n      initialize:\n        - run: true\n")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo.Root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("Link replaced a user-managed symlink")
	}
	if target, _ := os.Readlink(filepath.Join(repo.Root, "node_modules")); target != outside {
		t.Fatalf("user symlink changed to %q", target)
	}
}

func TestGroupedHookFailureRestoresOldVariant(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, "lock"), "v1")
	good := "resources:\n  - name: node\n    paths: [node_modules]\n    fingerprint: [\"{root}/lock\"]\n    hooks:\n      initialize:\n        - run: sh -c 'touch node_modules/complete'\n          cwd: \"{root}\"\n"
	writeConfig(t, repo.Root, config.Filename, good)
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo.Root, "node_modules")
	old, _ := os.Readlink(path)
	writeFile(t, filepath.Join(repo.Root, "lock"), "v2")
	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: node\n    paths: [node_modules]\n    fingerprint: [\"{root}/lock\"]\n    hooks:\n      initialize:\n        - run: sh -c 'touch node_modules/partial; exit 9'\n          cwd: \"{root}\"\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err == nil {
		t.Fatal("failing hook succeeded")
	}
	if target, _ := os.Readlink(path); target != old {
		t.Fatalf("link target = %q, want restored %q", target, old)
	}
	if _, err := os.Stat(filepath.Join(path, "complete")); err != nil {
		t.Fatalf("old variant unavailable: %v", err)
	}
}

func TestGroupedResourceDetachRelinkRoundTrip(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, "lock"), "v1")
	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: node\n    paths: [node_modules]\n    fingerprint: [\"{root}/lock\"]\n    hooks:\n      initialize:\n        - run: sh -c 'touch node_modules/installed'\n          cwd: \"{root}\"\n")
	options := Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}
	if err := Link(repo, options); err != nil {
		t.Fatal(err)
	}
	if err := Detach(repo, options); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(repo.Root, "node_modules")); err != nil {
		t.Fatal(err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("detached output is still a symlink: mode=%v", info.Mode())
	}
	if err := Relink(repo, options); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(repo.Root, "node_modules")); err != nil {
		t.Fatal(err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("relinked output is not a symlink: mode=%v", info.Mode())
	}
}

func TestGroupedHookChangedOutputRetainsStagingData(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)
	writeFile(t, filepath.Join(repo.Root, "lock"), "v1")
	writeConfig(t, repo.Root, config.Filename, "resources:\n  - name: node\n    paths: [node_modules]\n    fingerprint: [\"{root}/lock\"]\n    hooks:\n      initialize:\n        - run: sh -c 'rm node_modules; mkdir -p {shared}/custom; touch {shared}/custom/user-data; ln -s {shared}/custom node_modules'\n          cwd: \"{root}\"\n")
	if err := Link(repo, Options{StorageRoot: storage, Stdout: &bytes.Buffer{}}); err == nil {
		t.Fatal("hook that replaced a grouped output succeeded")
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "node_modules", "user-data")); err != nil {
		t.Fatalf("changed output's staging data was removed: %v", err)
	}
}
