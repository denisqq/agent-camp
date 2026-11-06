package clients

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denisqq/agent-camp/camp-cli/guide"
	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/denisqq/agent-camp/internal/credstore"
)

func chdirTemp(t *testing.T) string {
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(previous) })
	return dir
}

func testProfile(dir string) profiles.Profile {
	return profiles.Profile{
		Name:          "local",
		ServerURL:     "http://127.0.0.1:8090",
		UserID:        "user-1",
		AgentID:       "agent-1",
		CredentialRef: "local-agent-1",
		Launcher:      filepath.Join(dir, "camp"),
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMergePreservesOtherMCPServers(t *testing.T) {
	chdirTemp(t)
	dir := t.TempDir()
	existing := `{"mcpServers":{"other-tool":{"type":"stdio","command":"other"}},"settings":{"theme":"dark"}}`
	if err := os.WriteFile(".mcp.json", []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ClaudeCode, Scope{}, testProfile(dir), ""); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, ".mcp.json")
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other-tool"]; !ok {
		t.Fatal("other MCP server dropped")
	}
	if _, ok := servers["agent-camp"]; !ok {
		t.Fatal("camp entry missing")
	}
	if doc["settings"].(map[string]any)["theme"] != "dark" {
		t.Fatal("unrelated top-level settings dropped")
	}
	// A backup of the previous file was kept.
	if _, err := os.Stat(".mcp.json.camp-backup"); err != nil {
		t.Fatal("no backup of previous config")
	}
}

func TestInvalidConfigIsNotOverwritten(t *testing.T) {
	chdirTemp(t)
	dir := t.TempDir()
	if err := os.WriteFile(".mcp.json", []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ClaudeCode, Scope{}, testProfile(dir), ""); err == nil {
		t.Fatal("invalid config must not be overwritten")
	}
	body, _ := os.ReadFile(".mcp.json")
	if string(body) != `{not json` {
		t.Fatal("invalid config was modified")
	}
}

func TestRepeatInstallHasOneEntry(t *testing.T) {
	chdirTemp(t)
	dir := t.TempDir()
	profile := testProfile(dir)
	for i := 0; i < 3; i++ {
		if _, err := Install(ClaudeCode, Scope{}, profile, ""); err != nil {
			t.Fatal(err)
		}
	}
	doc := readJSON(t, ".mcp.json")
	servers := doc["mcpServers"].(map[string]any)
	count := 0
	for name := range servers {
		if strings.HasPrefix(name, "agent-camp") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one camp entry, got %d", count)
	}
}

func TestArgumentsAfterClientKeepValues(t *testing.T) {
	// A second install with a changed profile (e.g. new launcher path)
	// updates the camp entry without losing other servers or duplicating
	// arguments.
	chdirTemp(t)
	dir := t.TempDir()
	profile := testProfile(dir)
	if _, err := Install(Cursor, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	profile.Launcher = filepath.Join(dir, "camp-new")
	if _, err := Install(Cursor, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, filepath.Join(".cursor", "mcp.json"))
	entry := doc["mcpServers"].(map[string]any)["agent-camp"].(map[string]any)
	if entry["command"] != profile.Launcher {
		t.Fatalf("launcher not updated: %+v", entry)
	}
	args := entry["args"].([]any)
	if len(args) != 3 || args[0] != "bridge" {
		t.Fatalf("args corrupted: %+v", args)
	}
}

func TestPrivateProfileDoesNotLeakCredentials(t *testing.T) {
	dir := t.TempDir()
	creds := credstore.Store{Dir: filepath.Join(dir, "credentials")}
	profile := testProfile(dir)
	if err := creds.Save(profile.CredentialRef, "secret-token-value"); err != nil {
		t.Fatal(err)
	}
	if err := (profiles.Store{Dir: filepath.Join(dir, "profiles")}).Save(profile); err != nil {
		t.Fatal(err)
	}

	previous, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(previous) })
	if _, err := Install(ClaudeCode, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	// The token appears only in the private credential file (0600), never
	// in the client config or the profile record.
	for _, path := range []string{".mcp.json", filepath.Join(dir, "profiles", "profile-local.json")} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "secret-token-value") {
			t.Fatalf("%s leaked the credential", path)
		}
	}
	info, err := os.Stat(filepath.Join(dir, "credentials", "local-agent-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode: %v", info.Mode())
	}
	token, err := creds.Load(profile.CredentialRef)
	if err != nil || token != "secret-token-value" {
		t.Fatalf("credential round trip: %q %v", token, err)
	}
}

func TestInstalledGuideMatchesPlugin(t *testing.T) {
	// The embedded canonical guide and the distributed plugin copy must be
	// identical; drifting copies teach agents different workflows.
	wd, _ := os.Getwd()
	pluginPath := filepath.Join("..", "..", "plugins", "agent-camp", "skills", guide.Name, "SKILL.md")
	plugin, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("wd=%s path=%s: %v", wd, pluginPath, err)
	}
	if string(plugin) != guide.Markdown {
		t.Fatal("plugin SKILL.md drifted from camp-cli/guide/using-agent-camp.md; update both")
	}
}

func TestClientWithoutShellEnvironment(t *testing.T) {
	// The client entry must reference an absolute launcher path and carry
	// the profile name, so GUI-launched clients need no exported CAMP_*
	// environment and no shell PATH.
	chdirTemp(t)
	dir := t.TempDir()
	profile := testProfile(dir)
	if _, err := Install(ClaudeCode, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, ".mcp.json")
	entry := doc["mcpServers"].(map[string]any)["agent-camp"].(map[string]any)
	command, _ := entry["command"].(string)
	if !filepath.IsAbs(command) {
		t.Fatalf("launcher must be absolute for GUI clients: %q", command)
	}
	body, _ := os.ReadFile(".mcp.json")
	if strings.Contains(string(body), "CAMP_TOKEN") || strings.Contains(string(body), "${") {
		t.Fatalf("config still depends on exported environment: %s", body)
	}
}

func TestRelativeLauncherRejected(t *testing.T) {
	dir := t.TempDir()
	profile := testProfile(dir)
	profile.Launcher = "camp"
	if err := profile.Validate(); err == nil {
		t.Fatal("relative launcher accepted")
	}
}
