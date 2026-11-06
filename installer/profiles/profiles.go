// Package profiles stores named Camp server connections (the client side
// of onboarding): server address, stable user/agent IDs, and a reference
// to the credential in the private credential store. Profiles are created
// by the installer (setup/register) and consumed by the camp CLI
// (bridge, doctor, connect).
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Profile describes one named server connection.
type Profile struct {
	Name          string `json:"name"`
	ServerURL     string `json:"server_url"`
	UserID        string `json:"user_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	// Launcher is the absolute path of the client-facing command (camp
	// bridge), so GUI clients work without inheriting a shell PATH.
	Launcher string `json:"launcher,omitempty"`
}

// Validate rejects profiles that would silently point at nothing.
func (p Profile) Validate() error {
	if p.Name == "" {
		return errors.New("profile name is required")
	}
	if p.ServerURL == "" {
		return fmt.Errorf("profile %s: server_url is required", p.Name)
	}
	if p.Launcher != "" && !filepath.IsAbs(p.Launcher) {
		return fmt.Errorf("profile %s: launcher must be an absolute path", p.Name)
	}
	return nil
}

type Store struct{ Dir string }

// DefaultDir returns the per-user profile directory.
func DefaultDir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "camp", "profiles")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".camp", "profiles")
}

func (s Store) path(name string) string {
	return filepath.Join(s.Dir, "profile-"+name+".json")
}

// Save persists the profile with 0600 permissions, atomically.
func (s Store) Save(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(p.Name) + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(p.Name))
}

// Load reads a profile by name.
func (s Store) Load(name string) (Profile, error) {
	body, err := os.ReadFile(s.path(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Profile{}, os.ErrNotExist
		}
		return Profile{}, err
	}
	var p Profile
	if err := json.Unmarshal(body, &p); err != nil {
		return Profile{}, fmt.Errorf("profile %s is corrupt: %w", name, err)
	}
	return p, nil
}

// List returns saved profile names in directory order.
func (s Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if prefix, ok := stringsCutPrefix(name, "profile-"); ok {
			if rest, ok2 := stringsCutSuffix(prefix, ".json"); ok2 {
				out = append(out, rest)
			}
		}
	}
	return out, nil
}

func stringsCutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return s, false
}
func stringsCutSuffix(s, suffix string) (string, bool) {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}
