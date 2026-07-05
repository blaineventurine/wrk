package config

import "errors"

var (
	ErrConfigNotFound    = errors.New("configuration file not found")
	ErrConfigIsDirectory = errors.New("configuration path is a directory")
	ErrInvalidConfig     = errors.New("invalid configuration")
)
