package main

import (
	"testing"
)

// TestGCJSONFlagRegistered pins the --json flag wiring for `wrk gc`.
// The flag is the agent-facing on-ramp to a machine-readable
// plan+result envelope.
func TestGCJSONFlagRegistered(t *testing.T) {
	if gcCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on gcCmd")
	}
}

// TestGCFlagsYesRegistered mirrors the parity check across every
// destructive command's --yes wiring; adding --json must not have
// broken the existing prompt-bypass path.
func TestGCFlagsYesRegistered(t *testing.T) {
	long := gcCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on gcCmd")
	}
	short := gcCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on gcCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to gcYes)")
	}
}
