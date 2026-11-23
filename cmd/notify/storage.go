package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type storedConfig struct {
	Server string `json:"server"`
}

func configFilePath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "config.json")
}

func loadServer(statePath string) string {
	data, err := os.ReadFile(configFilePath(statePath))
	if err != nil {
		return ""
	}
	var cfg storedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Server)
}

func saveServer(statePath, server string) error {
	path := configFilePath(statePath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(storedConfig{Server: server}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
