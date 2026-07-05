package repository

import "fmt"

// VCS identifies the repository implementation to use.
type VCS string

const (
	Auto VCS = "auto"
	Git  VCS = "git"
	JJ   VCS = "jj"
)

func ParseVCS(value string) (VCS, error) {
	switch VCS(value) {
	case Auto, Git, JJ:
		return VCS(value), nil

	default:
		return "", fmt.Errorf(
			"unknown VCS %q (expected auto, git, or jj)",
			value,
		)
	}
}
