package main

import (
	"testing"
)

// TestForgetJSONFlagRegistered pins the --json flag wiring for
// `wrk forget`.
func TestForgetJSONFlagRegistered(t *testing.T) {
	if forgetCmd.Flags().Lookup("json") == nil {
		t.Fatal("--json flag not registered on forgetCmd")
	}
}

// TestForgetFlagsYesRegistered mirrors the parity check across
// every destructive command's --yes wiring.
func TestForgetFlagsYesRegistered(t *testing.T) {
	long := forgetCmd.Flags().Lookup("yes")
	if long == nil {
		t.Fatal("--yes flag not registered on forgetCmd")
	}
	short := forgetCmd.Flags().ShorthandLookup("y")
	if short == nil {
		t.Fatal("-y shorthand not registered on forgetCmd")
	}
	if long != short {
		t.Fatal("--yes and -y must be the same flag (bound to forgetYes)")
	}
}

// TestForgetFlagsForceRegistered pins that --force still resolves.
func TestForgetFlagsForceRegistered(t *testing.T) {
	if forgetCmd.Flags().Lookup("force") == nil {
		t.Fatal("--force flag not registered on forgetCmd")
	}
}
