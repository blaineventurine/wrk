package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
)

// TestLocalOverrideAddsResource pins the additive half of the
// shared+local merge contract: a resource that appears ONLY in
// .wrk.local.yml (different name than any shared entry) is appended
// to the effective config and materialized by Link alongside the
// shared entry. A regression here would either silently drop the
// local resource or block the shared one, both of which break the
// per-developer opt-in workflow the README documents.
func TestLocalOverrideAddsResource(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n",
	)
	writeConfig(t, repo.Root, config.LocalFilename,
		"resources:\n"+
			"  - name: cfg\n"+
			"    path: cfg.toml\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "shared=1\n")
	writeFile(t, filepath.Join(repo.Root, "cfg.toml"), "local=1\n")

	if err := Link(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	// Both resources must materialize: symlink at the workspace path
	// and real bytes in shared storage. If the merge silently dropped
	// the local-only entry, cfg.toml would still be a real file and
	// the shared cfg.toml would not exist.
	for _, tc := range []struct {
		name    string
		relPath string
		content string
	}{
		{name: "shared entry", relPath: ".env", content: "shared=1\n"},
		{name: "local-only entry", relPath: "cfg.toml", content: "local=1\n"},
	} {
		wsPath := filepath.Join(repo.Root, tc.relPath)
		info, err := os.Lstat(wsPath)
		if err != nil {
			t.Fatalf("%s: lstat %s: %v", tc.name, wsPath, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s: workspace %s is not a symlink; mode=%v",
				tc.name, tc.relPath, info.Mode())
		}
		sharedPath := filepath.Join(storage, repo.RepositoryID, tc.relPath)
		got, err := os.ReadFile(sharedPath)
		if err != nil {
			t.Fatalf("%s: read shared %s: %v", tc.name, sharedPath, err)
		}
		if string(got) != tc.content {
			t.Errorf("%s: shared content = %q, want %q",
				tc.name, got, tc.content)
		}
	}
}

// TestLocalOverrideReplacesByName pins the replacement half of the
// merge contract: a local entry whose Name matches a shared entry
// REPLACES that shared entry wholesale — the shared hook does NOT
// run. Uses distinct marker files per hook so the winning hook is
// observable through a side effect that only executes when the
// override took hold.
//
// If a regression made the shared entry survive the merge (or
// duplicated both entries under the same name), both markers would
// appear; if the local entry was silently dropped, only the shared
// marker would appear. The test discriminates all three outcomes.
func TestLocalOverrideReplacesByName(t *testing.T) {
	if _, err := os.Stat("/usr/bin/touch"); err != nil {
		if _, err2 := os.Stat("/bin/touch"); err2 != nil {
			t.Skip("touch(1) not available")
		}
	}

	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// Both entries name `node` at `node_modules` — the local hook must
	// win. Each hook creates its own uniquely-named marker beside the
	// shared file (and creates the shared file itself so Link can
	// symlink into it).
	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: touch {shared} {root}/.shared-hook-ran\n",
	)
	writeConfig(t, repo.Root, config.LocalFilename,
		"resources:\n"+
			"  - name: node\n"+
			"    path: node_modules\n"+
			"    hooks:\n"+
			"      initialize:\n"+
			"        - run: touch {shared} {root}/.local-hook-ran\n",
	)
	// No node_modules in the workspace — forces the initialize-hook
	// branch of the plan.

	if err := Link(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	localMarker := filepath.Join(repo.Root, ".local-hook-ran")
	sharedMarker := filepath.Join(repo.Root, ".shared-hook-ran")

	// The local hook's marker MUST exist — proof that the override
	// replaced the shared entry and its hook ran.
	if _, err := os.Stat(localMarker); err != nil {
		t.Errorf("local override's hook did not run: marker %s missing: %v",
			localMarker, err)
	}

	// The shared hook's marker MUST NOT exist — proof that the shared
	// entry was replaced (not co-executed, not surviving alongside).
	if _, err := os.Stat(sharedMarker); err == nil {
		t.Errorf("shared hook ran despite local override; marker %s should not exist",
			sharedMarker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat shared marker: %v", err)
	}
}

// TestLocalOverridePathRedirectPrintsWarning pins the S15 fix: when
// a local override redirects a shared resource's Path (not just its
// hook), Load surfaces a non-fatal warning and Link prints it to
// Stdout with the `!  ` gutter defined by printWarnings. This is the
// only user-visible signal that the effective path silently changed
// out from under the shared config — a regression that dropped the
// warning would let a bad override quietly break every hook that
// depended on the original path.
func TestLocalOverridePathRedirectPrintsWarning(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	writeConfig(t, repo.Root, config.Filename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n",
	)
	writeConfig(t, repo.Root, config.LocalFilename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: env.local\n",
	)
	// The override's redirected path needs a real file so Link can
	// materialize it — the point of this test is the warning line, not
	// the plan itself, so we make the plan succeed.
	writeFile(t, filepath.Join(repo.Root, "env.local"), "override\n")

	var out bytes.Buffer
	if err := Link(repo, Options{
		StorageRoot: storage,
		Stdout:      &out,
	}); err != nil {
		t.Fatalf("Link: %v", err)
	}

	printed := out.String()

	// Every warning line printWarnings emits starts with `!` + two
	// spaces (the plan-bullet gutter). Find that line and validate its
	// content in one pass so a regression that changes the prefix, the
	// message, or drops the line entirely is diagnosable from the
	// failure message.
	var warningLine string
	for _, line := range strings.Split(printed, "\n") {
		if strings.HasPrefix(line, "!  ") {
			warningLine = line
			break
		}
	}
	if warningLine == "" {
		t.Fatalf(
			"no `!  ` warning line in Link output; want a redirect advisory\n---\n%s",
			printed,
		)
	}

	// The merge helper renders the path redirect as
	//   local override for "env" redirects path from ".env" to "env.local"
	// (see internal/config/load.go merge()). Pin the resource name,
	// both paths, and the "redirects" verb so any of the four
	// substrings drifting is caught individually.
	for _, want := range []string{
		`"env"`,
		`".env"`,
		`"env.local"`,
		"redirects",
	} {
		if !strings.Contains(warningLine, want) {
			t.Errorf("warning line missing %q\nline: %s", want, warningLine)
		}
	}
}

// TestLocalOverrideOnlyWithoutShared pins the "personal project"
// workflow the README documents (see README §"local override"): a
// repo can carry ONLY .wrk.local.yml with no committed .wrk.yml, and
// wrk still loads and links every configured resource. A regression
// that made .wrk.yml mandatory would break this workflow silently
// (config.Load errors with ErrConfigNotFound) and Link would refuse
// to run.
func TestLocalOverrideOnlyWithoutShared(t *testing.T) {
	repo := newTestRepo(t)
	storage := storageIn(t, repo.Root)

	// No .wrk.yml on disk. Only the local file defines the resource.
	writeConfig(t, repo.Root, config.LocalFilename,
		"resources:\n"+
			"  - name: env\n"+
			"    path: .env\n",
	)
	writeFile(t, filepath.Join(repo.Root, ".env"), "local-only\n")

	// config.Load itself must succeed — the load path for a bare local
	// file is the second half of the workflow contract.
	cfg, err := config.Load(repo.Root)
	if err != nil {
		t.Fatalf("config.Load with only %s: %v", config.LocalFilename, err)
	}
	if len(cfg.Resources) != 1 || cfg.Resources[0].Name != "env" {
		t.Fatalf("Resources = %+v, want single entry named env", cfg.Resources)
	}

	if err := Link(repo, Options{
		StorageRoot: storage,
		Stdout:      &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Link with only %s: %v", config.LocalFilename, err)
	}

	// The workspace path is now a symlink into shared storage. If Link
	// silently no-op'd because it saw an "empty" config, .env would
	// still be a real file.
	info, err := os.Lstat(filepath.Join(repo.Root, ".env"))
	if err != nil {
		t.Fatalf("lstat .env: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("workspace .env is not a symlink; mode=%v", info.Mode())
	}
	sharedEnv := filepath.Join(storage, repo.RepositoryID, ".env")
	got, err := os.ReadFile(sharedEnv)
	if err != nil {
		t.Fatalf("read shared: %v", err)
	}
	if string(got) != "local-only\n" {
		t.Errorf("shared content = %q, want %q", got, "local-only\n")
	}
}
