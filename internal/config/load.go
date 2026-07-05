package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load reads the wrk configuration from the repository root.
func Load(root string) (*Config, error) {
	path := filepath.Join(root, Filename)

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"%w: %s",
				ErrConfigNotFound,
				path,
			)
		}

		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf(
			"%w: %s",
			ErrConfigIsDirectory,
			path,
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf(
			"%w: %v",
			ErrInvalidConfig,
			err,
		)
	}

	normalize(cfg)

	return cfg, nil
}

func normalize(cfg *Config) {
	if cfg.Resources == nil {
		cfg.Resources = []Resource{}
	}

	for i := range cfg.Resources {
		resource := &cfg.Resources[i]

		if resource.Fingerprint == nil {
			resource.Fingerprint = []string{}
		}

		if resource.Hooks == nil {
			resource.Hooks = make(map[string][]Command)
		}
	}
}
