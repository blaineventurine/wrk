package main

import (
	"testing"
)

// TestRemoveJSONFlagRegistered pins the --json flag wiring for
// `wrk remove`.
func TestRemoveJSONFlagRegistered(t *testing.T) {
	if removeCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on removeCmd")
	}
}

// TestRemoveFlagsYesRegistered mirrors the parity check across
// every destructive command's --yes wiring.
func TestRemoveFlagsYesRegistered(t *testing.T) {
	long := removeCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on removeCmd")
	}
	short := removeCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on removeCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to removeYes)")
	}
}

// TestRemoveFlagsForceRegistered pins that --force still resolves
// after the --json addition — the two flags must coexist so agents
// pairing --json --force can override refusals in one call.
func TestRemoveFlagsForceRegistered(t *testing.T) {
	if removeCmd.Flags().Lookup("force") == nil {
		t.Fatal("--force flag not registered on removeCmd")
	}
}
