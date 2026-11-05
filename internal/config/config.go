// Package config loads and saves the persistent Camp configuration file.
//
// Precedence when resolving a setting: built-in defaults, then the config
// file, then explicitly supplied environment overrides (CAMP_*), then CLI
// overrides. Environment variables are the automation interface, not the
// onboarding path; setup normally writes the file.
//
// The file holds no secret material: S3 credentials live in private files
// elsewhere and are referenced by name through CredentialsRef.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SchemaVersion is the on-disk configuration schema. Bump when the file
// format changes in a way old loaders cannot read.
const SchemaVersion = 1

// Storage describes where workspace objects live. Mode is "file" or "s3";
// both modes share the object key layout, so the root has the same meaning
// in either.
type Storage struct {
	Mode           string `json:"mode"`
	Root           string `json:"root,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	Bucket         string `json:"bucket,omitempty"`
	Region         string `json:"region,omitempty"`
	CredentialsRef string `json:"credentials_ref,omitempty"`
	// AccessKey/SecretKey are intentionally not serialized: secrets never
	// enter the configuration file. They exist only so callers that already
	// hold credentials (host environment) can pass them alongside the file.
	AccessKey string `json:"-"`
	SecretKey string `json:"-"`
}

// Server holds listener settings.
type Server struct {
	Listen string `json:"listen"`
}

// Registration is the server's agent-admission policy.
type Registration struct {
	// Mode is "disabled" (default) or "open".
	Mode             string `json:"mode"`
	StarterWorkspace string `json:"starter_workspace,omitempty"`
}

type fileFormat struct {
	SchemaVersion int          `json:"schema_version"`
	Server        Server       `json:"server"`
	Storage       Storage      `json:"storage"`
	Registration  Registration `json:"registration"`
}

// Config is the resolved, validated configuration with all paths absolute.
type Config struct {
	SchemaVersion int
	Listen        string
	Storage       Storage
	// DataRoot is the parent of Storage.Root; control and index files live
	// there by default.
	DataRoot    string
	ControlPath string
	IndexPath   string
	// Runner python/script/monty paths (absolute once resolved).
	RunnerPython string
	RunnerScript string
	RunnerMonty  string
	Runner       int
	Registration Registration
	// Path is the file this configuration was loaded from ("" for pure
	// defaults) and Base the directory relative paths resolve against.
	Path string
	Base string
}

// Overrides carries explicit CLI values; empty strings are ignored.
type Overrides struct {
	Listen       string
	StorageMode  string
	StorageRoot  string
	ControlPath  string
	IndexPath    string
	RunnerPython string
	RunnerScript string
	RunnerMonty  string
}

// Defaults returns the default configuration rooted at dataRoot (typically
// the sibling "camp-data" directory of the config file).
func Defaults(dataRoot string) Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Listen:        "127.0.0.1:8090",
		Storage:       Storage{Mode: "file", Root: filepath.Join(dataRoot, "objects")},
		DataRoot:      dataRoot,
		ControlPath:   filepath.Join(dataRoot, "control.sqlite"),
		IndexPath:     filepath.Join(dataRoot, "index.sqlite"),
		RunnerPython:  "./runner/.venv/bin/python",
		RunnerScript:  "./runner/camp_runner.py",
		RunnerMonty:   "./runner/.venv/bin/monty",
		Runner:        2,
		Registration:  Registration{Mode: "disabled", StarterWorkspace: "workspace"},
	}
}

// UserPath returns the default configuration file path for the current OS
// user (e.g. ~/.config/camp/camp.json).
func UserPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "camp", "camp.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".camp", "camp.json")
}

// Load resolves the configuration at path. A missing file yields defaults;
// any other error (unreadable, invalid JSON, unknown mode) fails with an
// error naming the setting and, where possible, the setup command that can
// repair it.
func Load(path string, o Overrides) (Config, error) {
	base := filepath.Dir(path)
	defaults := Defaults(filepath.Join(base, "camp-data"))
	cfg := defaults
	cfg.Path = path
	cfg.Base = base

	var ff fileFormat
	if body, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(body, &ff); err != nil {
			return Config{}, fmt.Errorf("camp setup storage: invalid configuration file %s: %w", path, err)
		}
		if ff.SchemaVersion > SchemaVersion {
			return Config{}, fmt.Errorf("configuration %s was written by a newer Camp (schema %d > %d); upgrade camp", path, ff.SchemaVersion, SchemaVersion)
		}
		if ff.Server.Listen != "" {
			cfg.Listen = ff.Server.Listen
		}
		if ff.Storage.Mode != "" {
			cfg.Storage.Mode = ff.Storage.Mode
		}
		if ff.Storage.Root != "" {
			cfg.Storage.Root = ff.Storage.Root
		}
		cfg.Storage.Endpoint = ff.Storage.Endpoint
		cfg.Storage.Bucket = ff.Storage.Bucket
		cfg.Storage.Region = ff.Storage.Region
		cfg.Storage.CredentialsRef = ff.Storage.CredentialsRef
		if ff.Registration.Mode != "" {
			cfg.Registration.Mode = ff.Registration.Mode
		}
		if ff.Registration.StarterWorkspace != "" {
			cfg.Registration.StarterWorkspace = ff.Registration.StarterWorkspace
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("read configuration %s: %w", path, err)
	}

	// Environment overrides (automation interface).
	env := func(key string) string { return os.Getenv(key) }
	if v := env("CAMP_LISTEN"); v != "" {
		cfg.Listen = v
	}
	if v := env("CAMP_STORE"); v != "" {
		cfg.Storage.Mode = v
	}
	if v := env("CAMP_ROOT"); v != "" {
		cfg.Storage.Root = filepath.Join(v, "objects")
		cfg.DataRoot = v
	}
	if v := env("CAMP_CONTROL_PATH"); v != "" {
		cfg.ControlPath = v
	}
	if v := env("CAMP_INDEX_PATH"); v != "" {
		cfg.IndexPath = v
	}
	if v := env("CAMP_RUNNER_PYTHON"); v != "" {
		cfg.RunnerPython = v
	}
	if v := env("CAMP_RUNNER_SCRIPT"); v != "" {
		cfg.RunnerScript = v
	}
	if v := env("CAMP_MONTY_BINARY"); v != "" {
		cfg.RunnerMonty = v
	}

	// CLI overrides win over everything.
	if o.Listen != "" {
		cfg.Listen = o.Listen
	}
	if o.StorageMode != "" {
		cfg.Storage.Mode = o.StorageMode
	}
	if o.StorageRoot != "" {
		cfg.Storage.Root = o.StorageRoot
	}
	if o.ControlPath != "" {
		cfg.ControlPath = o.ControlPath
	}
	if o.IndexPath != "" {
		cfg.IndexPath = o.IndexPath
	}
	if o.RunnerPython != "" {
		cfg.RunnerPython = o.RunnerPython
	}
	if o.RunnerScript != "" {
		cfg.RunnerScript = o.RunnerScript
	}
	if o.RunnerMonty != "" {
		cfg.RunnerMonty = o.RunnerMonty
	}

	// When the storage root moved (file/env/CLI), the control and index
	// defaults follow it unless they were set explicitly.
	if cfg.Storage.Root != defaults.Storage.Root && cfg.ControlPath == defaults.ControlPath {
		cfg.ControlPath = filepath.Join(filepath.Dir(cfg.Storage.Root), "control.sqlite")
	}
	if cfg.Storage.Root != defaults.Storage.Root && cfg.IndexPath == defaults.IndexPath {
		cfg.IndexPath = filepath.Join(filepath.Dir(cfg.Storage.Root), "index.sqlite")
	}

	// Resolve every path against the config file's directory so both
	// storage modes see the same absolute root regardless of the caller's
	// working directory.
	if !filepath.IsAbs(cfg.Storage.Root) {
		cfg.Storage.Root = filepath.Join(base, cfg.Storage.Root)
	}
	if !filepath.IsAbs(cfg.DataRoot) {
		cfg.DataRoot = filepath.Join(base, cfg.DataRoot)
	}
	if !filepath.IsAbs(cfg.ControlPath) {
		cfg.ControlPath = filepath.Join(base, cfg.ControlPath)
	}
	if !filepath.IsAbs(cfg.IndexPath) {
		cfg.IndexPath = filepath.Join(base, cfg.IndexPath)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Storage.Mode {
	case "file":
	case "s3":
		if c.Storage.Endpoint == "" {
			return errors.New(`camp setup storage: storage.mode "s3" requires storage.endpoint`)
		}
		if c.Storage.Bucket == "" {
			return errors.New(`camp setup storage: storage.mode "s3" requires storage.bucket`)
		}
	default:
		return fmt.Errorf("camp setup storage: unknown storage.mode %q (want \"file\" or \"s3\")", c.Storage.Mode)
	}
	switch c.Registration.Mode {
	case "disabled", "open":
	default:
		return fmt.Errorf("camp setup: unknown registration.mode %q (want \"disabled\" or \"open\")", c.Registration.Mode)
	}
	if c.Registration.Mode == "open" && c.Registration.StarterWorkspace == "" {
		return errors.New(`camp setup: registration.mode "open" requires registration.starter_workspace`)
	}
	return nil
}

// Save validates cfg and persists it atomically at path: the file is
// written to a temporary sibling, then renamed, so an interrupted save
// leaves the previous working configuration in place. Storage paths are
// stored relative to the file when they already live under its directory,
// keeping the file free of machine-specific absolute paths.
func Save(path string, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	base := filepath.Dir(path)
	rel := func(p string) string {
		if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
		return p
	}
	ff := fileFormat{
		SchemaVersion: SchemaVersion,
		Server:        Server{Listen: cfg.Listen},
		Storage: Storage{
			Mode:           cfg.Storage.Mode,
			Root:           rel(cfg.Storage.Root),
			Endpoint:       cfg.Storage.Endpoint,
			Bucket:         cfg.Storage.Bucket,
			Region:         cfg.Storage.Region,
			CredentialsRef: cfg.Storage.CredentialsRef,
		},
		Registration: cfg.Registration,
	}
	body, err := json.MarshalIndent(ff, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(base, ".camp-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// DiscoverPath picks the configuration file: an explicit CAMP_CONFIG, a
// camp.json in the working directory, or the OS user configuration
// directory. When the user file does not exist either, Load on that path
// still yields the historical defaults via its fallback handling in the
// CLI. Shared by both binaries.
func DiscoverPath() string {
	if p := os.Getenv("CAMP_CONFIG"); p != "" {
		return p
	}
	if _, err := os.Stat("camp.json"); err == nil {
		return "camp.json"
	}
	return UserPath()
}
