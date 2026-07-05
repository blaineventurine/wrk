package app

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

func DefaultStorage() string {
	return filepath.Join(
		xdg.DataHome,
		"wrk",
	)
}
