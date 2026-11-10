// Client integration adapters. Each adapter merges Camp-owned entries
// into the client's MCP configuration and installs the workflow skill,
// preserving every other setting the user (or other tools) put there.
package clients

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/denisqq/agent-camp/installer/profiles"
)

// Client identifies a supported agent client.
type Client struct {
	// Key: "claude-code", "claude-desktop", "codex", "cursor".
	Key string
}

// Scope selects user-wide or project-local installation.
type Scope struct {
	Project bool
	// Root is the project directory for Project scope (required) and
	// ignored otherwise.
	Root string
}

// InstallResult reports what happened. State is "configured" (client files
// updated; the client may still need to reload before tools are live) or
// "pending-reload" — never a claim that tools are already connected.
type InstallResult struct {
	State         string   `json:"state"`
	Files         []string `json:"files"`
	Detail        string   `json:"detail,omitempty"`
	BootstrapMode string   `json:"bootstrap_mode"`
}

// Supported clients.
var (
	ClaudeCode       = Client{"claude-code"}
	ClaudeDesktop    = Client{"claude-desktop"}
	Codex            = Client{"codex"}
	Cursor           = Client{"cursor"}
	SupportedClients = []Client{ClaudeCode, ClaudeDesktop, Codex, Cursor}
)

// Install merges the Camp MCP entry and workflow skill for one client.
// ServerName defaults to "agent-camp".
func Install(client Client, scope Scope, profile profiles.Profile, serverName string) (InstallResult, error) {
	plan, err := prepareInstall(client, scope, profile, serverName)
	if err != nil {
		return InstallResult{}, err
	}
	return applyInstall(plan)
}

func bridgeServer(profile profiles.Profile) map[string]any {
	entry := map[string]any{
		"type":    "stdio",
		"command": profile.Launcher,
		"args":    []string{"bridge", "--profile", profile.Name},
	}
	return entry
}

// installClaudeDesktop prints the merge snippet: the desktop config is a
// single JSON file the app owns; Camp never rewrites it silently.
func installClaudeDesktop(profile profiles.Profile, serverName string) (InstallResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return InstallResult{}, err
	}
	path := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	entry, _ := json.MarshalIndent(map[string]any{serverName: bridgeServer(profile)}, "", "  ")
	return InstallResult{State: "configured", Detail: fmt.Sprintf("merge into %s:\n%s", path, entry)}, nil
}
