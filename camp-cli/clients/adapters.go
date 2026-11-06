// Client integration adapters. Each adapter merges Camp-owned entries
// into the client's MCP configuration and installs the workflow skill,
// preserving every other setting the user (or other tools) put there.
package clients

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/denisqq/agent-camp/camp-cli/guide"
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
	State  string   `json:"state"`
	Files  []string `json:"files"`
	Detail string   `json:"detail,omitempty"`
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
	if serverName == "" {
		serverName = "agent-camp"
	}
	if err := profile.Validate(); err != nil {
		return InstallResult{}, err
	}
	switch client.Key {
	case "claude-code":
		return installJSONClient(scope.Root, ".mcp.json", ".claude/skills", profile, serverName, scope.Project)
	case "cursor":
		return installJSONClient(scope.Root, filepath.Join(".cursor", "mcp.json"), filepath.Join(".cursor", "skills"), profile, serverName, scope.Project)
	case "codex":
		return installCodex(profile, serverName)
	case "claude-desktop":
		return installClaudeDesktop(profile, serverName)
	default:
		return InstallResult{}, fmt.Errorf("unknown client %q", client.Key)
	}
}

// installJSONClient covers Claude Code and Cursor: a JSON file with an
// mcpServers object. Only our server name is touched; other servers and
// unknown keys survive byte-for-byte semantics (re-marshaled JSON).
func installJSONClient(projectRoot, configRel, skillsRel string, profile profiles.Profile, serverName string, project bool) (InstallResult, error) {
	if project && projectRoot == "" {
		return InstallResult{}, fmt.Errorf("project scope requires a root directory")
	}
	if projectRoot == "" {
		projectRoot = "."
	}
	// The config file lives under projectRoot (the project root for
	// project scope, the working directory for user scope of file-based
	// clients like Claude Code).
	base := projectRoot
	configPath := filepath.Join(base, configRel)

	var doc map[string]any
	if body, err := os.ReadFile(configPath); err == nil {
		if !json.Valid(body) {
			return InstallResult{}, fmt.Errorf("%s is not valid JSON; leaving it untouched (repair it or remove it and rerun)", configPath)
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return InstallResult{}, fmt.Errorf("%s is not a JSON object file; leaving it untouched", configPath)
		}
	} else if !os.IsNotExist(err) {
		return InstallResult{}, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		if existing, ok := doc["mcpServers"]; ok && existing != nil {
			return InstallResult{}, fmt.Errorf("%s has an mcpServers key of an unexpected type; leaving it untouched", configPath)
		}
		servers = map[string]any{}
	}
	servers[serverName] = bridgeServer(profile)
	doc["mcpServers"] = servers
	if err := mergeWriteJSON(configPath, doc); err != nil {
		return InstallResult{}, err
	}

	skillsDir := filepath.Join(base, skillsRel)
	skillPath := filepath.Join(skillsDir, guide.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return InstallResult{}, err
	}
	if err := os.WriteFile(skillPath, []byte(guide.Markdown), 0o644); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{State: "pending-reload", Files: []string{configPath, skillPath}, Detail: "reconnect/reload the client to load the MCP server"}, nil
}

func bridgeServer(profile profiles.Profile) map[string]any {
	entry := map[string]any{
		"type":    "stdio",
		"command": profile.Launcher,
		"args":    []string{"bridge", "--profile", profile.Name},
	}
	return entry
}

func mergeWriteJSON(path string, doc map[string]any) error {
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		// Back up the previous file before replacing it.
		_ = os.WriteFile(path+".camp-backup", existing, 0o600)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

var codexBlockRe = regexp.MustCompile(`(?m)^\[mcp_servers\.` + regexp.QuoteMeta("agent-camp") + `\]\n(?:(?:[^\[]|\[\[).*?\n)*`)

// installCodex merges the Camp block into ~/.codex/config.toml, replacing
// only previous Camp-owned blocks and preserving every other table.
func installCodex(profile profiles.Profile, serverName string) (InstallResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return InstallResult{}, err
	}
	config := filepath.Join(home, ".codex", "config.toml")
	existing, _ := os.ReadFile(config)
	text := string(existing)

	block := fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [%q, %q, %q]\n", serverName, profile.Launcher, "bridge", "--profile", profile.Name)
	if strings.Contains(text, "[mcp_servers."+serverName+"]") {
		re := regexp.MustCompile(`(?ms)^\[mcp_servers\.` + regexp.QuoteMeta(serverName) + `\]\n.*?(?=^\[|\z)`)
		text = re.ReplaceAllString(text, block)
	} else {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n" + block
	}
	if len(existing) > 0 {
		_ = os.WriteFile(config+".camp-backup", existing, 0o600)
	}
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		return InstallResult{}, err
	}
	if err := os.WriteFile(config, []byte(text), 0o600); err != nil {
		return InstallResult{}, err
	}
	skillPath := filepath.Join(home, ".codex", "skills", guide.Name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		return InstallResult{}, err
	}
	if err := os.WriteFile(skillPath, []byte(guide.Markdown), 0o644); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{State: "pending-reload", Files: []string{config, skillPath}, Detail: "restart codex to load the MCP server"}, nil
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
