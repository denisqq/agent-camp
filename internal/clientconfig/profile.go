// Package clientconfig manages private Camp client profiles and installs
// client integrations without clobbering unrelated settings.
//
// A profile names one Camp server connection: the server address, the
// stable user/agent IDs established at registration, and a reference to
// the credential stored in private local storage (never in client config
// files, never in repositories).
package clientconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Profile describes one named server connection. Token lives in the
// private credential store; configs and logs only ever see CredentialRef.
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

// Store is the private profile directory (user configuration directory by
// default; tests inject a temp dir).
type Store struct{ Dir string }

// DefaultStoreDir returns the per-user private Camp profile directory.
func DefaultStoreDir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "camp", "profiles")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".camp", "profiles")
}

func (s Store) profilePath(name string) string {
	return filepath.Join(s.Dir, "profile-"+name+".json")
}
func (s Store) credentialPath(ref string) string {
	return filepath.Join(s.Dir, "credentials", ref+".json")
}

// SaveProfile persists the profile with 0600 permissions.
func (s Store) SaveProfile(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return writePrivate(s.profilePath(p.Name), p)
}

// LoadProfile reads a profile by name.
func (s Store) LoadProfile(name string) (Profile, error) {
	var p Profile
	body, err := os.ReadFile(s.profilePath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Profile{}, os.ErrNotExist
		}
		return Profile{}, err
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Profile{}, fmt.Errorf("profile %s is corrupt: %w", name, err)
	}
	return p, nil
}

// SaveCredential stores the token privately under its reference. The ref,
// not the token, appears in profiles and client configs.
func (s Store) SaveCredential(ref, token string) error {
	if ref == "" || token == "" {
		return errors.New("credential reference and token are required")
	}
	return writePrivate(s.credentialPath(ref), map[string]string{"token": token})
}

// LoadCredential resolves a credential reference to its token.
func (s Store) LoadCredential(ref string) (string, error) {
	body, err := os.ReadFile(s.credentialPath(ref))
	if err != nil {
		return "", err
	}
	var cred map[string]string
	if err := json.Unmarshal(body, &cred); err != nil {
		return "", err
	}
	if cred["token"] == "" {
		return "", errors.New("credential store entry has no token")
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
