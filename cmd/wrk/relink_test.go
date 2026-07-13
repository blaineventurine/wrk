package main

import (
	"strings"
	"testing"

	"github.com/blaineventurine/wrk/internal/config"
	"github.com/blaineventurine/wrk/internal/engine"
)

// TestRelinkFlagsYesRegistered pins the flag wiring: both --yes and
// -y point at relinkYes. If the short form ever drifts, users typing
// `wrk relink -y` in a rush would silently trip the unknown-flag
// error, which is exactly the failure mode --yes is meant to prevent.
func TestRelinkFlagsYesRegistered(t *testing.T) {
	long := relinkCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on relinkCmd")
	}
	short := relinkCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on relinkCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to relinkYes)")
	}
}

// TestRelinkFlagsForceRegistered pins that `wrk relink --force`
// resolves — parity with `wrk gc/forget/remove/detach/run --force`.
// The wire matters for two reasons: users who habitually type
// --force expect it to work, and the plan-first flow depends on
// Confirm receiving Force to render the override banner uniformly.
func TestRelinkFlagsForceRegistered(t *testing.T) {
	if relinkCmd.Flags().Lookup("force") == nil {
		t.Fatal("--force flag not registered on relinkCmd")
	}
}

// TestRelinkIsolateFlagRegistered pins that --isolate is wired on
// relinkCmd. If this drifts, the whole isolate CLI surface silently
// disappears — `wrk relink --isolate` would fail as "unknown flag"
// after already having survived through review.
func TestRelinkIsolateFlagRegistered(t *testing.T) {
	if relinkCmd.Flags().Lookup("isolate") == nil {
		t.Fatal("--isolate flag not registered on relinkCmd")
	}
}

// TestRelinkArgsRejectsPositionalWithoutIsolate pins the positional-
// args guard: `wrk relink node` without --isolate is a typo, not a
// scoped relink, and MUST be rejected before RunE ever touches the
// engine. The relinkIsolate package-global is snapshot/restored to
// avoid test-order dependence on other tests that flip the flag.
func TestRelinkArgsRejectsPositionalWithoutIsolate(t *testing.T) {
	old := relinkIsolate
	defer func() { relinkIsolate = old }()
	relinkIsolate = false

	err := relinkCmd.Args(relinkCmd, []string{"node"})
	if err == nil {
		t.Fatal("expected error for positional arg without --isolate")
	}
	if !strings.Contains(err.Error(), "--isolate") {
		t.Fatalf("error should reference --isolate so users know the fix, got: %v", err)
	}
}

// TestRelinkArgsAcceptsPositionalWithIsolate pins the other half of
// the guard: with --isolate set, one or many positional resource
// names are the whole point of the flag. Zero args are also legal
// (means "isolate every detached resource"), so we verify all three.
func TestRelinkArgsAcceptsPositionalWithIsolate(t *testing.T) {
	old := relinkIsolate
	defer func() { relinkIsolate = old }()
	relinkIsolate = true

	for _, args := range [][]string{
		{},
		{"node"},
		{"node", "env"},
	} {
		if err := relinkCmd.Args(relinkCmd, args); err != nil {
			t.Errorf("unexpected error for args %v with --isolate: %v", args, err)
		}
	}
}

// TestPrintIsolatePlanLists asserts the local plan formatter renders
// every resource on its own line. This is the only path users see
// what --isolate will do before answering the prompt; if the loop
// drops entries, the prompt lies about scope.
func TestPrintIsolatePlanLists(t *testing.T) {
	var buf strings.Builder

	// engine.IsolatePlan lives in the engine package; construct via a
	// zero-value helper so this test does not couple to the internal
	// resource shape beyond Name and Path.
	plan := makeIsolatePlanFixture(t, [][2]string{
		{"node", "node_modules"},
		{"env", ".env"},
	})

	printIsolatePlan(&buf, plan)
	got := buf.String()

	for _, want := range []string{
		"node (node_modules)",
		"env (.env)",
		"Files stay under shared storage",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("printIsolatePlan output missing %q; got:\n%s", want, got)
		}
	}
}

// makeIsolatePlanFixture builds a minimal engine.IsolatePlan for
// display-only unit tests. The plan fields not exercised by
// printIsolatePlan (Root) are left at zero values on purpose so the
// test asserts only what the formatter reads.
func makeIsolatePlanFixture(t *testing.T, pairs [][2]string) engine.IsolatePlan {
	t.Helper()
	resources := make([]config.Resource, 0, len(pairs))
	for _, p := range pairs {
		resources = append(resources, config.Resource{Name: p[0], Path: p[1]})
	}
	return engine.IsolatePlan{Resources: resources}
}

// TestRelinkJSONFlagRegistered pins the --json flag wiring for
// `wrk relink` and `wrk relink --isolate`. Both flavors share the
// same flag var; drift would silently break both paths.
func TestRelinkJSONFlagRegistered(t *testing.T) {
	if relinkCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on relinkCmd")
	}
}
