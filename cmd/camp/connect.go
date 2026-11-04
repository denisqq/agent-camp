package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// camp connect installs the camp integration into a specific agent harness:
// MCP server config plus a skill that teaches the agent the camp contract.
// Modeled after how plugin packs install per-harness: one command, real files
// in the right places, nothing copied by hand. --print shows the same config
// instead of writing it.
func connect(args []string) error {
	// Reorder so flags may come after the harness name: camp connect codex --print.
	flagsFirst := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flagsFirst = append(flagsFirst, a)
		} else {
			rest = append(rest, a)
		}
	}
	args = append(flagsFirst, rest...)
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	url := flags.String("url", env("CAMP_MCP_URL", "http://127.0.0.1:8090/mcp"), "camp MCP endpoint")
	tokenEnv := flags.String("token-env", "CAMP_TOKEN", "environment variable holding the bearer token")
	name := flags.String("name", "agent-camp", "server name in client configs")
	printOnly := flags.Bool("print", false, "print config instead of installing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: camp connect <claude-code|claude-desktop|codex|cursor|generic> [--print]")
	}
	c := connector{url: *url, tokenEnv: *tokenEnv, name: *name, printOnly: *printOnly}
	switch flags.Arg(0) {
	case "claude-code":
		return c.claudeCode()
	case "claude-desktop":
		return c.claudeDesktop()
	case "codex":
		return c.codex()
	case "cursor":
		return c.cursor()
	case "generic":
		fmt.Println(c.jsonConfig())
		return nil
	default:
		return fmt.Errorf("unknown harness %q", flags.Arg(0))
	}
}

type connector struct {
	url, tokenEnv, name string
	printOnly           bool
}

func (c connector) jsonConfig() string {
	cfg := map[string]any{"mcpServers": map[string]any{
		c.name: map[string]any{"type": "http", "url": c.url, "headers": map[string]string{"Authorization": "Bearer ${" + c.tokenEnv + "}"}},
	}}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b) + "\n"
}
func (c connector) tomlConfig() string {
	return fmt.Sprintf("[mcp_servers.%s]\nurl = %q\nbearer_token_env_var = %q\n", c.name, c.url, c.tokenEnv)
}

func (c connector) write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func (c connector) installSkill(dir string) error {
	return c.write(filepath.Join(dir, "using-agent-camp", "SKILL.md"), skillMarkdown)
}

func (c connector) claudeCode() error {
	if c.printOnly {
		fmt.Println(c.jsonConfig())
		return nil
	}
	if err := c.write(".mcp.json", c.jsonConfig()); err != nil {
		return err
	}
	if err := c.installSkill(".claude/skills"); err != nil {
		return err
	}
	fmt.Printf("Installed .mcp.json and .claude/skills/using-agent-camp in %s\n", mustWd())
	fmt.Println("Set " + c.tokenEnv + " before launching Claude Code. Alternative: /plugin marketplace add denisqq/agent-camp")
	return nil
}
func (c connector) claudeDesktop() error {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	fmt.Printf("Merge into %s (or use --print for the snippet):\n%s\n", path, c.jsonConfig())
	fmt.Println("Set " + c.tokenEnv + " in the environment Claude Desktop starts with.")
	return nil
}
func (c connector) codex() error {
	if c.printOnly {
		fmt.Println(c.tomlConfig())
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	config := filepath.Join(home, ".codex", "config.toml")
	existing, _ := os.ReadFile(config)
	block := c.tomlConfig()
	if strings.Contains(string(existing), "mcp_servers."+c.name) {
		fmt.Printf("%s already configures %s; left untouched\n", config, c.name)
	} else if err = c.write(config, string(existing)+"\n"+block); err != nil {
		return err
	}
	if err = c.installSkill(filepath.Join(home, ".codex", "skills")); err != nil {
		return err
	}
	fmt.Printf("Configured %s and installed the skill into ~/.codex/skills\n", config)
	fmt.Println("Set " + c.tokenEnv + " (e.g. in your shell profile) before running codex.")
	return nil
}
func (c connector) cursor() error {
	if c.printOnly {
		fmt.Println(c.jsonConfig())
		return nil
	}
	if err := c.write(filepath.Join(".cursor", "mcp.json"), c.jsonConfig()); err != nil {
		return err
	}
	if err := c.installSkill(filepath.Join(".cursor", "skills")); err != nil {
		return err
	}
	fmt.Printf("Installed .cursor/mcp.json and .cursor/skills/using-agent-camp in %s\n", mustWd())
	return nil
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

const skillMarkdown = `---
name: using-agent-camp
description: Use when the shared agent workspace (agent-camp) is available — discovering spaces, reading and writing documents, discussions with other agents, handling conflicts and retries.
---

# Working with the shared workspace (agent-camp)

You have access to a camp — a shared, versioned workspace for agents.

* Discover first: call the MCP tool ` + "`map`" + ` with no arguments to see your
  spaces and allowed actions, then ` + "`map`" + ` with ` + "`space=<id>`" + ` for the
  layout (directories, kinds, link hubs) before reading anything.
* Open a session with ` + "`start`" + ` (primary_space from discovery), then use
  ` + "`run`" + ` with Python: ` + "`from camp_sdk import camp`" + `. Help lives inside:
  ` + "`camp.help()`" + `, ` + "`camp.help('documents')`" + `.
* Read rules and summaries before bodies; check ` + "`coverage`" + ` — a truncated
  listing is not the whole picture.
* Write with the same path to keep identity: documents are versioned, writes
  in one run commit atomically or not at all. On ` + "`conflict`" + ` someone else
  committed first: refresh your snapshot, re-apply, retry with a new
  operation id. Retrying the same operation id with identical code is safe
  and returns the original result.
* Talk to other agents by leaving ` + "`comment(path, text, to='agent')`" + `
  messages in discussions — nobody is online; check what is new in
  /discussions since your last session.
* Prefer ` + "`map`" + `, ` + "`find(kind=..., metadata=...)`" + `, ` + "`backlinks`" + ` and
  line-ranged ` + "`read`" + ` over loading whole documents into context.
* The last expression of your run becomes the JSON result returned to the
  caller; keep it small.
`
