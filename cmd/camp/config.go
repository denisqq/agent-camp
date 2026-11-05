package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/denisqq/agent-camp/internal/config"
)

// printConfig reports the resolved configuration without secrets: S3 keys
// are never part of the file, only referenced by name.
func printConfig(cfg config.Config) error {
	out := struct {
		SchemaVersion int               `json:"schema_version"`
		Path          string            `json:"path"`
		Listen        string            `json:"listen"`
		Storage       config.Storage    `json:"storage"`
		ControlPath   string            `json:"control_path"`
		IndexPath     string            `json:"index_path"`
		Registration  config.Registration `json:"registration"`
	}{cfg.SchemaVersion, cfg.Path, cfg.Listen, cfg.Storage, cfg.ControlPath, cfg.IndexPath, cfg.Registration}
	out.Storage.AccessKey, out.Storage.SecretKey = "", ""
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	fmt.Println("configuration errors mention the setting and the setup command that repairs it; secrets are stored separately")
	return nil
}

// configPath picks the configuration file: an explicit CAMP_CONFIG, a
// camp.json in the working directory, or the OS user configuration
// directory. When none exists, loadConfig falls back to pure defaults so
// the historical environment-variable workflow keeps working unchanged.
func configPath() string {
	if p := os.Getenv("CAMP_CONFIG"); p != "" {
		return p
	}
	if _, err := os.Stat("camp.json"); err == nil {
		return "camp.json"
	}
	return config.UserPath()
}

func loadConfig(o config.Overrides) (config.Config, error) {
	path := configPath()
	if path == config.UserPath() {
		if _, err := os.Stat(path); err != nil {
			// No configuration anywhere: historical defaults rooted at the
			// working directory, still overridable through CAMP_*.
			return config.Load(filepath.Join(wd(), "camp.json"), o)
		}
	}
	return config.Load(path, o)
}

func wd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}
