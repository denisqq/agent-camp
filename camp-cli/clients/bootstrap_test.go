package clients

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProjectBootstrapInstallIdempotent(t *testing.T) {
	for _, client := range []Client{Codex, Cursor, ClaudeCode} {
		t.Run(client.Key, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			original := "# Project\n\nKeep existing instructions.\n"
			if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0755); err != nil {
				t.Fatal(err)
			}
			settings := `{"permissions":{"allow":["Read"]},"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"other-hook"}]}]}}`
			if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(settings), 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := Install(client, Scope{Project: true, Root: dir}, testProfile(dir), ""); err != nil {
					t.Fatal(err)
				}
			}
			switch client.Key {
			case "codex":
				body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(string(body), original) || strings.Count(string(body), "<!-- camp:bootstrap:start -->") != 1 {
					t.Fatalf("instructions lost or duplicated: %s", body)
				}
				if _, err := os.Stat(filepath.Join(dir, ".codex", "config.toml")); err != nil {
					t.Fatal(err)
				}
			case "cursor":
				body, err := os.ReadFile(filepath.Join(dir, ".cursor", "rules", "agent-camp.mdc"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(body), "alwaysApply: true") {
					t.Fatalf("rule not always applied: %s", body)
				}
			case "claude-code":
				doc := readJSON(t, filepath.Join(dir, ".claude", "settings.json"))
				if doc["permissions"].(map[string]any)["allow"].([]any)[0] != "Read" {
					t.Fatal("permissions lost")
				}
				hooks := doc["hooks"].(map[string]any)["SessionStart"].([]any)
				if len(hooks) != 2 {
					t.Fatalf("want original hook plus Camp hook, got %v", hooks)
				}
				if hooks[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"] != "other-hook" {
					t.Fatal("original hook changed")
				}
			}
		})
	}
}

func TestBootstrapMalformedFilesUntouched(t *testing.T) {
	for _, tc := range []struct {
		name       string
		client     Client
		file, body string
	}{
		{"claude-json", ClaudeCode, ".claude/settings.json", "{bad json"},
		{"claude-hooks-type", ClaudeCode, ".claude/settings.json", `{"hooks":[]}`},
		{"codex-unclosed", Codex, "AGENTS.md", "text\n<!-- camp:bootstrap:start -->\nold"},
		{"cursor-unowned", Cursor, ".cursor/rules/agent-camp.mdc", "my custom rule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(dir, tc.file)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(tc.client, Scope{Project: true, Root: dir}, testProfile(dir), ""); err == nil {
				t.Fatal("malformed/unowned bootstrap accepted")
			}
			body, _ := os.ReadFile(path)
			if string(body) != tc.body {
				t.Fatal("original modified")
			}
			config := ".mcp.json"
			if tc.client == Cursor {
				config = ".cursor/mcp.json"
			}
			if tc.client == Codex {
				config = ".codex/config.toml"
			}
			if _, err := os.Stat(filepath.Join(dir, config)); !os.IsNotExist(err) {
				t.Fatalf("config written before validation: %v", err)
			}
		})
	}
}

func TestBootstrapSymlinkOutsideScope(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".cursor", "rules")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Cursor, Scope{Project: true, Root: dir}, testProfile(dir), ""); err == nil {
		t.Fatal("external symlink accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside project")
	}
}

func TestCodexReinstallPreservesNestedTables(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0755); err != nil {
		t.Fatal(err)
	}
	original := "[mcp_servers.other]\ncommand = \"other\"\n[mcp_servers.other.env]\nKEEP = \"yes\"\n"
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Install(Codex, Scope{}, testProfile(dir), ""); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := os.ReadFile(config)
	if !strings.HasPrefix(string(body), original) || strings.Count(string(body), "[mcp_servers.agent-camp]") != 1 {
		t.Fatalf("TOML damaged: %s", body)
	}
}

func TestClaudeHookLauncherArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture; Windows launcher requires a native executable")
	}
	dir := t.TempDir()
	launcher := filepath.Join(dir, "camp with ' $HOME `special` characters")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := testProfile(dir)
	profile.Launcher = launcher
	if _, err := Install(ClaudeCode, Scope{Project: true, Root: dir}, profile, ""); err != nil {
		t.Fatal(err)
	}
	doc := readJSON(t, filepath.Join(dir, ".claude", "settings.json"))
	entry := doc["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)
	handler := entry["hooks"].([]any)[0].(map[string]any)
	args := []string{}
	if raw, ok := handler["args"].([]any); ok {
		for _, arg := range raw {
			args = append(args, arg.(string))
		}
	}
	output, err := exec.Command(handler["command"].(string), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v: %s", err, output)
	}
	if string(output) != "bootstrap\n--format\nclaude-code\n" {
		t.Fatalf("arguments changed: %q", output)
	}
}

func TestCodexOverrideAndUserInstallScope(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	override := filepath.Join(dir, "AGENTS.override.md")
	original := "# Override\nKeep me.\n"
	if err := os.WriteFile(override, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Codex, Scope{Project: true, Root: dir}, testProfile(dir), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("created shadowed AGENTS.md")
	}
	body, _ := os.ReadFile(override)
	if !strings.HasPrefix(string(body), original) {
		t.Fatal("override content lost")
	}
	// Machine onboarding must not modify instruction files in the working project.
	before := string(body)
	if _, err := Install(Codex, Scope{}, testProfile(dir), ""); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(override)
	if string(body) != before {
		t.Fatal("user install changed project instructions")
	}
}

func TestBootstrapTargetChangedToSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	plan, err := prepareInstall(Cursor, Scope{Project: true, Root: dir}, testProfile(dir), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(dir, ".cursor")); err != nil {
		t.Fatal(err)
	}
	if _, err = applyInstall(plan); err == nil {
		t.Fatal("redirected installation accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("installation escaped project: %v", entries)
	}
}

func TestBootstrapPreviewDoesNotExposeExistingSecrets(t *testing.T) {
	dir := t.TempDir()
	original := `{"mcpServers":{"other":{"command":"other","env":{"API_KEY":"PRIVATE-OTHER-KEY"}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	files, _, err := Preview(ClaudeCode, Scope{Project: true, Root: dir}, testProfile(dir), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.Contains(file.Content, "PRIVATE-OTHER-KEY") {
			t.Fatal("preview exposes unrelated credential")
		}
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if string(body) != original {
		t.Fatal("preview modified original")
	}
}

func TestCodexWhitespaceTableReinstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	config := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(config), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("[mcp_servers . agent-camp]\ncommand = \"old\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Codex, Scope{}, testProfile(dir), ""); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(config)
	if strings.Contains(string(body), "command = \"old\"") {
		t.Fatalf("duplicate old table retained: %s", body)
	}
}

func TestCodexEmptyOverrideKeepsEffectiveInstructions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.override.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Follow project rules.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Codex, Scope{Project: true, Root: dir}, testProfile(dir), ""); err != nil {
		t.Fatal(err)
	}
	override, _ := os.ReadFile(filepath.Join(dir, "AGENTS.override.md"))
	if len(override) != 0 {
		t.Fatal("empty override now shadows project instructions")
	}
	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agents), blockStart) {
		t.Fatal("effective instructions lack bootstrap")
	}
}

func isolatedClientHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func TestUserBootstrapWorksWithoutProject(t *testing.T) {
	for _, client := range []Client{ClaudeCode, Codex, Cursor} {
		t.Run(client.Key, func(t *testing.T) {
			home := isolatedClientHome(t)
			work := t.TempDir()
			t.Chdir(work)
			profile := testProfile(home)
			for i := 0; i < 2; i++ {
				if _, err := Install(client, Scope{Root: work}, profile, ""); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := os.ReadDir(work)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("user install wrote in task directory: %v", entries)
			}
			var config, instructions, skill string
			switch client {
			case ClaudeCode:
				config = ".claude.json"
				instructions = ".claude/settings.json"
				skill = ".claude/skills/using-agent-camp/SKILL.md"
			case Codex:
				config = ".codex/config.toml"
				instructions = ".codex/AGENTS.md"
				skill = ".agents/skills/using-agent-camp/SKILL.md"
			case Cursor:
				config = ".cursor/mcp.json"
				instructions = ".cursor/hooks.json"
				skill = ".cursor/skills/using-agent-camp/SKILL.md"
			}
			for _, rel := range []string{config, instructions, skill} {
				body, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
				if err != nil || len(body) == 0 {
					t.Fatalf("missing user integration %s: %v", rel, err)
				}
			}
			switch client {
			case ClaudeCode:
				doc := readJSON(t, filepath.Join(home, ".claude", "settings.json"))
				hooks := doc["hooks"].(map[string]any)["SessionStart"].([]any)
				if len(hooks) != 1 {
					t.Fatalf("duplicate user hooks: %v", hooks)
				}
			case Cursor:
				doc := readJSON(t, filepath.Join(home, ".cursor", "hooks.json"))
				hooks := doc["hooks"].(map[string]any)["sessionStart"].([]any)
				if len(hooks) != 1 {
					t.Fatalf("duplicate user hooks: %v", hooks)
				}
			}
		})
	}
}

func TestUserBootstrapPreviewDoesNotWrite(t *testing.T) {
	for _, client := range []Client{ClaudeCode, Codex, Cursor} {
		t.Run(client.Key, func(t *testing.T) {
			home := isolatedClientHome(t)
			work := t.TempDir()
			t.Chdir(work)
			files, result, err := Preview(client, Scope{}, testProfile(home), "")
			if err != nil {
				t.Fatal(err)
			}
			if result.BootstrapMode == "skill-only" || len(files) < 3 {
				t.Fatalf("startup delivery missing: %+v", result)
			}
			for _, file := range files {
				if !strings.HasPrefix(file.Path, home+string(filepath.Separator)) {
					t.Fatalf("non-user destination %s", file.Path)
				}
			}
			entries, _ := os.ReadDir(home)
			if len(entries) != 0 {
				t.Fatal("preview wrote into home")
			}
			entries, _ = os.ReadDir(work)
			if len(entries) != 0 {
				t.Fatal("preview wrote into task directory")
			}
		})
	}
}

func TestGlobalCodexPreservesOverride(t *testing.T) {
	home := isolatedClientHome(t)
	base := filepath.Join(home, ".codex")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	original := "# Personal preferences\nAlways follow my preferences.\n"
	if err := os.WriteFile(filepath.Join(base, "AGENTS.override.md"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Codex, Scope{}, testProfile(home), ""); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(base, "AGENTS.override.md"))
	if !strings.HasPrefix(string(body), original) || !strings.Contains(string(body), blockStart) {
		t.Fatal("effective user instructions lost or not extended")
	}
}

func TestUserCursorHookRunsOutsideProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	home := isolatedClientHome(t)
	work := t.TempDir()
	t.Chdir(work)
	launcher := filepath.Join(home, "camp ' $HOME `special` launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	profile := testProfile(home)
	profile.Launcher = launcher
	if _, err := Install(Cursor, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	hooks := readJSON(t, filepath.Join(home, ".cursor", "hooks.json"))["hooks"].(map[string]any)["sessionStart"].([]any)
	command := hooks[0].(map[string]any)["command"].(string)
	output, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed outside project: %v %s", err, output)
	}
	if string(output) != "bootstrap\n--format\ncursor\n" {
		t.Fatalf("launcher arguments changed: %q", output)
	}
	entries, _ := os.ReadDir(work)
	if len(entries) != 0 {
		t.Fatal("hook wrote to unrelated task directory")
	}
}

func TestUserCursorPreservesOtherHooksAndRejectsMalformed(t *testing.T) {
	home := isolatedClientHome(t)
	base := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"version":1,"custom":"keep","hooks":{"sessionStart":[{"command":"other-start","timeout":12}],"stop":[{"command":"other-stop"}]}}`
	path := filepath.Join(base, "hooks.json")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Install(Cursor, Scope{}, testProfile(home), ""); err != nil {
			t.Fatal(err)
		}
	}
	doc := readJSON(t, path)
	hooks := doc["hooks"].(map[string]any)
	if doc["custom"] != "keep" || len(hooks["sessionStart"].([]any)) != 2 || hooks["sessionStart"].([]any)[0].(map[string]any)["command"] != "other-start" || hooks["stop"].([]any)[0].(map[string]any)["command"] != "other-stop" {
		t.Fatalf("unrelated hooks lost: %v", doc)
	}
	for _, body := range []string{`{"version":2,"hooks":{}}`, `{"version":1,"hooks":{"sessionStart":{}}}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(filepath.Join(base, "mcp.json"))
		if _, err := Install(Cursor, Scope{}, testProfile(home), ""); err == nil {
			t.Fatal("unsupported/malformed hooks accepted")
		}
		after, _ := os.ReadFile(filepath.Join(base, "mcp.json"))
		if string(before) != string(after) {
			t.Fatal("MCP config changed before hook validation")
		}
		after, _ = os.ReadFile(path)
		if string(after) != body {
			t.Fatal("malformed hook config overwritten")
		}
	}
}

func TestUserClientCustomConfigDirectories(t *testing.T) {
	for _, client := range []Client{ClaudeCode, Codex} {
		t.Run(client.Key, func(t *testing.T) {
			home := isolatedClientHome(t)
			custom := filepath.Join(t.TempDir(), "new", "client-profile")
			env := "CODEX_HOME"
			config := "config.toml"
			instruction := "AGENTS.md"
			if client == ClaudeCode {
				env = "CLAUDE_CONFIG_DIR"
				config = ".claude.json"
				instruction = "settings.json"
			}
			t.Setenv(env, custom)
			if _, err := Install(client, Scope{}, testProfile(home), ""); err != nil {
				t.Fatal(err)
			}
			for _, rel := range []string{config, instruction} {
				if _, err := os.Stat(filepath.Join(custom, rel)); err != nil {
					t.Fatalf("custom config ignored: %s: %v", rel, err)
				}
			}
			defaultDir := ".codex"
			if client == ClaudeCode {
				defaultDir = ".claude"
			}
			if _, err := os.Stat(filepath.Join(home, defaultDir)); !os.IsNotExist(err) {
				t.Fatalf("default client profile modified: %v", err)
			}
		})
	}
}

func TestCustomClientRootRedirectedAfterPreview(t *testing.T) {
	home := isolatedClientHome(t)
	parent := t.TempDir()
	custom := filepath.Join(parent, "new-profile")
	outside := t.TempDir()
	t.Setenv("CODEX_HOME", custom)
	plan, err := prepareInstall(Codex, Scope{}, testProfile(home), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, custom); err != nil {
		t.Fatal(err)
	}
	if _, err = applyInstall(plan); err == nil {
		t.Fatal("redirected custom client root accepted")
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("wrote outside selected profile")
	}
}

func TestUserCursorRepairsExecutableScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable permissions")
	}
	home := isolatedClientHome(t)
	profile := testProfile(home)
	if _, err := Install(Cursor, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(home, ".cursor", "hooks", "agent-camp-bootstrap.sh")
	if err := os.Chmod(script, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Cursor, Scope{}, profile, ""); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Fatal("startup script remains non-executable after reinstall")
	}
}

func TestGlobalCodexInlineServerEntriesFailBeforeWrites(t *testing.T) {
	for _, body := range []string{
		"[mcp_servers]\nagent-camp = { command = \"old\", args = [] }\n",
		"[mcp_servers]\nagent-camp.command = \"old\"\n",
		"[mcp_servers]\n\"agent-camp\" = { command = \"old\" }\n",
	} {
		t.Run(body, func(t *testing.T) {
			home := isolatedClientHome(t)
			base := filepath.Join(home, ".codex")
			if err := os.MkdirAll(base, 0700); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(base, "config.toml")
			if err := os.WriteFile(config, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(Codex, Scope{}, testProfile(home), ""); err == nil {
				t.Fatal("ambiguous inline server accepted and may be duplicated")
			}
			after, _ := os.ReadFile(config)
			if string(after) != body {
				t.Fatal("existing TOML modified")
			}
			if _, err := os.Stat(filepath.Join(base, "AGENTS.md")); !os.IsNotExist(err) {
				t.Fatal("instructions written before config validation")
			}
		})
	}
}
