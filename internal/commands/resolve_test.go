package commands

import (
	"reflect"
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
