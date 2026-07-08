package repository

import "testing"

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
