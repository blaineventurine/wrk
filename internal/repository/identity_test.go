package repository

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestParseRemoteNormalizes locks in the identity contract: every
// spelling of the same clone URL must produce the same ID, so that
// `git clone` variants converge on shared workspace storage.
func TestParseRemoteNormalizes(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "scp-syntax",
			url:  "git@github.com:org/repo.git",
			want: "github.com/org/repo",
		},
		{
			name: "https canonical",
			url:  "https://github.com/org/repo.git",
			want: "github.com/org/repo",
		},
		{
			name: "https mixed-case host preserves path case",
			url:  "https://GitHub.com/Org/Repo.git",
			want: "github.com/Org/Repo",
		},
		{
			name: "ssh with default port stripped",
			url:  "ssh://git@github.com:22/org/repo.git",
			want: "github.com/org/repo",
		},
		{
			name: "ssh without port",
			url:  "ssh://git@github.com/org/repo.git",
			want: "github.com/org/repo",
		},
		{
			name: "https default port 443 stripped",
			url:  "https://gitlab.example.com:443/team/proj.git",
			want: "gitlab.example.com/team/proj",
		},
		{
			name: "http default port 80 stripped",
			url:  "http://gitlab.example.com:80/team/proj.git",
			want: "gitlab.example.com/team/proj",
		},
		{
			name: "ssh non-default port preserved",
			url:  "ssh://git@example.com:2222/team/proj.git",
			want: "example.com:2222/team/proj",
		},
		{
			name: "junk input",
			url:  "not a url at all",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRemote(tc.url); got != tc.want {
				t.Fatalf("parseRemote(%q) = %q, want %q",
					tc.url, got, tc.want)
			}
		})
	}
}

// TestParseRemoteRejectsMalformed guards the two failure paths that
// callers rely on to fall back to the local hash: syntactically valid
// but semantically empty URLs must return "".
func TestParseRemoteRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"scp missing colon", "git@github.com"},
		{"scp empty path", "git@github.com:"},
		{"https no host", "https:///org/repo.git"},
		{"https no path", "https://github.com"},
		{"https root-only path", "https://github.com/"},
		{"unknown scheme", "ftp://example.com/repo.git"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRemote(tc.url); got != "" {
				t.Fatalf("parseRemote(%q) = %q, want empty",
					tc.url, got)
			}
		})
	}
}

// TestPreferredRemoteFallbacks pins S11: identity resolution walks
// origin -> upstream -> the sole other remote before giving up.
// Fork-first workflows (upstream but no origin) and ad-hoc setups (a
// single remote called "gh" or "gl") should still converge on a
// remote-derived ID instead of falling back to the local hash.
func TestPreferredRemoteFallbacks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Isolate git from the user's global config: an insteadOf rule
	// would rewrite our fixture URLs and break exact-match asserts.
	// GIT_CONFIG_GLOBAL=/dev/null empties the global scope;
	// GIT_CONFIG_NOSYSTEM=1 disables /etc/gitconfig. Neither is on
	// capture's strip list, so both survive sanitizedEnv.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// initRepo sets up an empty git repo with the given remotes.
	// A nil URL means don't add the remote at all.
	initRepo := func(t *testing.T, remotes map[string]string) string {
		t.Helper()
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
			t.Fatalf("git init: %v", err)
		}
		for name, url := range remotes {
			if err := exec.Command("git", "-C", root, "remote", "add", name, url).Run(); err != nil {
				t.Fatalf("git remote add %s: %v", name, err)
			}
		}
		return root
	}

	t.Run("origin missing, upstream present", func(t *testing.T) {
		root := initRepo(t, map[string]string{
			"upstream": "https://github.com/org/upstream-repo.git",
		})
		if got := preferredRemoteURL(root, filepath.Join(root, ".git")); got != "https://github.com/org/upstream-repo.git" {
			t.Fatalf("preferredRemoteURL = %q, want upstream URL", got)
		}
	})

	t.Run("origin preferred over upstream", func(t *testing.T) {
		root := initRepo(t, map[string]string{
			"origin":   "https://github.com/org/origin-repo.git",
			"upstream": "https://github.com/org/upstream-repo.git",
		})
		if got := preferredRemoteURL(root, filepath.Join(root, ".git")); got != "https://github.com/org/origin-repo.git" {
			t.Fatalf("preferredRemoteURL = %q, want origin URL", got)
		}
	})

	t.Run("single non-standard remote falls back", func(t *testing.T) {
		root := initRepo(t, map[string]string{
			"gh": "https://github.com/org/only-remote.git",
		})
		if got := preferredRemoteURL(root, filepath.Join(root, ".git")); got != "https://github.com/org/only-remote.git" {
			t.Fatalf("preferredRemoteURL = %q, want gh URL", got)
		}
	})

	t.Run("multiple non-standard remotes give up", func(t *testing.T) {
		root := initRepo(t, map[string]string{
			"gh": "https://github.com/org/one.git",
			"gl": "https://gitlab.com/org/two.git",
		})
		if got := preferredRemoteURL(root, filepath.Join(root, ".git")); got != "" {
			t.Fatalf("preferredRemoteURL = %q, want empty (ambiguous)", got)
		}
	})

	t.Run("no remotes at all gives up", func(t *testing.T) {
		root := initRepo(t, nil)
		if got := preferredRemoteURL(root, filepath.Join(root, ".git")); got != "" {
			t.Fatalf("preferredRemoteURL = %q, want empty", got)
		}
	})
}
