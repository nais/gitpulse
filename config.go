package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Username  string   `toml:"username"`
	Orgs      []string `toml:"orgs"`
	Repos     []string `toml:"repos"`
	LocalDirs []string `toml:"local_dirs"`
}

func loadConfig(path string) (*Config, error) {
	if path == "" {
		path = findConfigFile()
	}
	if path == "" {
		return nil, fmt.Errorf("no config file found; create config.toml or use --config")
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if cfg.Username == "" {
		return nil, fmt.Errorf("username is required in config")
	}
	if len(cfg.Repos) == 0 {
		return nil, fmt.Errorf("at least one repo is required in config")
	}

	return &cfg, nil
}

func findConfigFile() string {
	candidates := []string{
		"config.toml",
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "gitpulse", "config.toml"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
