package commands

import (
	"reflect"
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/placeholders"
)

func context() placeholders.Context {
	return placeholders.Context{
		Root:   "/repo",
		Parent: "/repo/apps/web",
		Match:  "/repo/apps/web/node_modules",
		Shared: "/cache/node_modules/abc123",
	}
}

func TestResolveRun(t *testing.T) {
	resolved, err := Resolve(
		[]config.Command{
			{
				Run: `echo "{shared}"`,
			},
		},
		context(),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"echo",
		"/cache/node_modules/abc123",
	}

	if !reflect.DeepEqual(resolved[0].Args, want) {
		t.Fatalf(
			"got %#v\nwant %#v",
			resolved[0].Args,
			want,
		)
	}
}

func TestResolveCwd(t *testing.T) {
	resolved, err := Resolve(
		[]config.Command{
			{
				Run: "pwd",
				Cwd: "{parent}",
			},
		},
		context(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if resolved[0].Cwd != "/repo/apps/web" {
		t.Fatalf(
			"got %q\nwant %q",
			resolved[0].Cwd,
			"/repo/apps/web",
		)
	}
}

func TestResolveEnvironment(t *testing.T) {
	resolved, err := Resolve(
		[]config.Command{
			{
				Run: "bundle install",
				Env: map[string]string{
					"BUNDLE_PATH": "{shared}",
					"ROOT":        "{root}",
				},
			},
		},
		context(),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"BUNDLE_PATH": "/cache/node_modules/abc123",
		"ROOT":        "/repo",
	}

	if !reflect.DeepEqual(resolved[0].Env, want) {
		t.Fatalf(
			"got %#v\nwant %#v",
			resolved[0].Env,
			want,
		)
	}
}

func TestDefaultCwdIsRoot(t *testing.T) {
	resolved, err := Resolve(
		[]config.Command{
			{
				Run: "pwd",
			},
		},
		context(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if resolved[0].Cwd != "/repo" {
		t.Fatalf(
			"got %q\nwant %q",
			resolved[0].Cwd,
			"/repo",
		)
	}
}

func TestResolveQuotedArguments(t *testing.T) {
	resolved, err := Resolve(
		[]config.Command{
			{
				Run: `echo "hello world"`,
			},
		},
		context(),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"echo",
		"hello world",
	}

	if !reflect.DeepEqual(resolved[0].Args, want) {
		t.Fatalf(
			"got %#v\nwant %#v",
			resolved[0].Args,
			want,
		)
	}
}

func TestResolveReturnsErrorForInvalidCommand(t *testing.T) {
	_, err := Resolve(
		[]config.Command{
			{
				Run: `echo "unterminated`,
			},
		},
		context(),
	)

	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestInputCommandsNotModified(t *testing.T) {
	command := config.Command{
		Run: "echo {root}",
		Cwd: "{shared}",
		Env: map[string]string{
			"ROOT": "{root}",
		},
	}

	_, err := Resolve(
		[]config.Command{command},
		context(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if command.Run != "echo {root}" {
		t.Fatalf(
			"Run modified\n got %q\nwant %q",
			command.Run,
			"echo {root}",
		)
	}

	if command.Cwd != "{shared}" {
		t.Fatalf(
			"Cwd modified\n got %q\nwant %q",
			command.Cwd,
			"{shared}",
		)
	}

	if command.Env["ROOT"] != "{root}" {
		t.Fatalf(
			"Env modified\n got %q\nwant %q",
			command.Env["ROOT"],
			"{root}",
		)
	}
}

// Resolve rejects `run:` strings that tokenize into unquoted shell
// operators (`&&`, `|`, `>`, ...). Direct exec would pass them as
// literal args to the first binary, producing confusing errors like
// "sleep: invalid time interval: &&". The user needs `sh -c "..."`.
func TestResolveRejectsShellOperators(t *testing.T) {
	cases := []string{
		"sleep 3 && mkdir -p /tmp/x",
		"cat a.txt | grep foo",
		"echo done > /tmp/marker",
		"echo x ; echo y",
		"cmd 2>&1",
		"a || b",
	}
	for _, run := range cases {
		t.Run(run, func(t *testing.T) {
			_, err := Resolve([]config.Command{{Run: run}}, context())
			if err == nil {
				t.Fatalf("Resolve(%q) succeeded; want shell-operator rejection", run)
			}
			if !strings.Contains(err.Error(), "sh -c") {
				t.Errorf("error %q missing 'sh -c' hint", err.Error())
			}
		})
	}
}

// Wrapping the command in `sh -c "..."` is the escape hatch. Metachars
// inside the quoted script are not standalone args after shlex, so
// Resolve accepts them.
func TestResolveAcceptsShellWrapped(t *testing.T) {
	_, err := Resolve(
		[]config.Command{{Run: `sh -c "sleep 3 && mkdir -p /tmp/x"`}},
		context(),
	)
	if err != nil {
		t.Fatalf("sh -c wrapper should be accepted: %v", err)
	}
}
