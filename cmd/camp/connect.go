package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// connect emits native MCP configuration for popular agents and a skill text
// that teaches the agent the camp contract (map → start → run). Configs never
// embed tokens: they reference the CAMP_TOKEN environment variable.
func connect(args []string) error {
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	format := flags.String("format", "claude-code", "claude-code|claude-desktop|codex|cursor|generic")
	url := flags.String("url", "http://127.0.0.1:8090/mcp", "camp MCP endpoint")
	name := flags.String("name", "agent-camp", "server name in the client config")
	write := flags.String("write", "", "write config to this path instead of printing")
	skill := flags.Bool("skill", false, "print an AGENTS.md/SKILL.md instruction block for the agent")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *skill {
		fmt.Print(skillText)
		return nil
	}
	var out string
	switch *format {
	case "claude-code", "cursor", "claude-desktop", "generic":
		cfg := map[string]any{"mcpServers": map[string]any{
			*name: map[string]any{"type": "http", "url": *url, "headers": map[string]string{"Authorization": "Bearer ${CAMP_TOKEN}"}},
		}}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return err
		}
		out = string(b) + "\n"
		if *format == "claude-desktop" {
			out = "# Claude Desktop: merge into claude_desktop_config.json; set CAMP_TOKEN before launch.\n" + out
		}
	case "codex":
		out = fmt.Sprintf("# Codex CLI: merge into ~/.codex/config.toml\n[mcp_servers.%s]\nurl = %q\nbearer_token_env_var = \"CAMP_TOKEN\"\n", *name, *url)
	default:
		return fmt.Errorf("unknown format %q", *format)
	}
	if *write != "" {
		return os.WriteFile(*write, []byte(out), 0o644)
	}
	fmt.Print(out)
	return nil
}

const skillText = `# Working with the shared workspace (agent-camp)

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
* The last expression of your run becomes the JSON result returned to the
  caller; keep it small.
`
