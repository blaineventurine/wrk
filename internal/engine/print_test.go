package engine

import (
	"fmt"
	"strings"
	"testing"
)

// TestDescribeActionUnknownPanics pins M16: an unrecognized action
// type is a programmer bug — someone added a case in planner without
// wiring it into the printer — so describeAction must panic loudly
// rather than emit a raw "%T" placeholder into the plan preview.
//
// A nil planner.Action satisfies the interface but matches none of
// the concrete cases, so the switch falls to the default branch,
// which is exactly the code path this test needs to exercise.
func TestDescribeActionUnknownPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("describeAction(nil) did not panic; want panic on unknown action type")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "describeAction missing case") {
			t.Fatalf("panic = %q, want mention of 'describeAction missing case'", msg)
		}
	}()
	_, _ = describeAction(nil)
}
