package engine

import (
	"fmt"
	"io"

	"github.com/blaineventurine/wrk/internal/config"
)

// printWarnings surfaces every non-fatal advisory the config Load
// produced. It is safe to call with a nil writer or a config that has
// no warnings — both are no-ops. The `!` prefix distinguishes warnings
// from the informational plan output the same command writes.
func printWarnings(cfg *config.Config, w io.Writer) {
	if cfg == nil || w == nil {
		return
	}
	for _, warning := range cfg.Warnings {
		fmt.Fprintf(w, "!  %s\n", warning)
	}
}
