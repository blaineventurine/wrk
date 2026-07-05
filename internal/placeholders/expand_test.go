package placeholders

import "testing"

func TestExpandRoot(t *testing.T) {
	ctx := Context{
		Root: "/repo",
	}

	got := Expand("{root}/package.json", ctx)

	want := "/repo/package.json"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandParent(t *testing.T) {
	ctx := Context{
		Parent: "/repo/apps/web",
	}

	got := Expand("{parent}/package.json", ctx)

	want := "/repo/apps/web/package.json"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandMatch(t *testing.T) {
	ctx := Context{
		Match: "/repo/node_modules",
	}

	got := Expand("{match}", ctx)

	want := "/repo/node_modules"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandShared(t *testing.T) {
	ctx := Context{
		Shared: "/cache/node_modules/abc123",
	}

	got := Expand("{shared}", ctx)

	want := "/cache/node_modules/abc123"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandMultiple(t *testing.T) {
	ctx := Context{
		Root:   "/repo",
		Shared: "/cache",
	}

	got := Expand(
		"{root} -> {shared}",
		ctx,
	)

	want := "/repo -> /cache"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnknownPlaceholderIsLeftUnchanged(t *testing.T) {
	got := Expand(
		"{unknown}",
		Context{},
	)

	want := "{unknown}"

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
