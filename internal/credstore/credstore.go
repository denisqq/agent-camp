// Package credstore persists credentials in private per-user files
// (0600). Shared by the client CLI, the installer, and camp-server's
// setup: everyone references credentials by name; the token itself never
// appears in configuration files or client configs.
package credstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct{ Dir string }

// DefaultDir is the per-user private credential directory.
func DefaultDir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "camp", "credentials")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".camp", "credentials")
}

// Save stores the token privately under its reference. The ref, not the
// token, appears in profiles and client configs.
func (s Store) Save(ref, token string) error {
	if ref == "" || token == "" {
		return errors.New("credential reference and token are required")
	}
	return writePrivate(filepath.Join(s.Dir, ref+".json"), map[string]string{"token": token})
}

// Load resolves a credential reference to its token.
func (s Store) Load(ref string) (string, error) {
	body, err := os.ReadFile(filepath.Join(s.Dir, ref+".json"))
	if err != nil {
		return "", err
	}
	var cred map[string]string
	if err := json.Unmarshal(body, &cred); err != nil {
		return "", err
	}
	if cred["token"] == "" {
		return "", fmt.Errorf("credential %s has no token", ref)
	}
	return cred["token"], nil
}

func writePrivate(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
