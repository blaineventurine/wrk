package main

import (
	"math"
	"testing"

	"github.com/blaineventurine/wrk/internal/engine"
)

// TestHumanSizeClamps pins the M3 defensive clamp: an input large
// enough to walk past the last defined SI suffix ('E') must fall back
// to that suffix instead of indexing past the "KMGTPE" string. No real
// filesystem produces such a size, but the arithmetic path allows it,
// and a panic here would take down `wrk list --size`.
func TestHumanSizeClamps(t *testing.T) {
	// math.MaxInt64 bytes is a lot more than an exabyte; without the
	// clamp, humanSize would step exp to 6+ and index out of range.
	got := engine.HumanSize(math.MaxInt64)
	if got == "" {
		t.Fatalf("humanSize(MaxInt64) returned empty string")
	}
	// The last defined suffix is 'E' — verify we clamped to it.
	if got[len(got)-2:] != "EB" {
		t.Fatalf("humanSize(MaxInt64) = %q, want …EB (clamped to exabytes)", got)
	}
}

// TestHumanSizeSpotChecks fixes a few well-known values so a regression
// in the loop itself is caught alongside the clamp.
func TestHumanSizeSpotChecks(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1 KB"},
		{-1, "-"},
	}
	for _, c := range cases {
		if got := engine.HumanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
