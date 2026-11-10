package clients

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/denisqq/agent-camp/installer/profiles"
)

func mergeManagedBlock(old, body []byte) ([]byte, error) {
	text := string(old)
	starts, ends := strings.Count(text, blockStart), strings.Count(text, blockEnd)
	block := blockStart + "\n" + strings.TrimSpace(string(body)) + "\n" + blockEnd
	if starts == 0 && ends == 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return []byte(text + "\n" + block + "\n"), nil
	}
	if starts != 1 || ends != 1 {
		return nil, fmt.Errorf("malformed or duplicate Camp instruction markers")
	}
	start, end := strings.Index(text, blockStart), strings.Index(text, blockEnd)
	if end < start {
		return nil, fmt.Errorf("reversed Camp instruction markers")
	}
	return []byte(text[:start] + block + text[end+len(blockEnd):]), nil
}

func jsonObject(old []byte) (map[string]any, error) {
	if len(old) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(old, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON; leaving file untouched: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return doc, nil
}
func objectField(doc map[string]any, key string) (map[string]any, error) {
	value, exists := doc[key]
	if !exists {
		return map[string]any{}, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return obj, nil
}
func marshalConfig(doc map[string]any) ([]byte, error) {
	body, err := json.MarshalIndent(doc, "", "  ")
	return append(body, '\n'), err
}

func mergeClaudeHook(old []byte, launcher string) ([]byte, error) {
	doc, err := jsonObject(old)
	if err != nil {
		return nil, err
	}
	hooks, err := objectField(doc, "hooks")
	if err != nil {
		return nil, err
	}
	var entries []any
	if value, ok := hooks["SessionStart"]; ok {
		var valid bool
		entries, valid = value.([]any)
		if !valid {
			return nil, fmt.Errorf("SessionStart must be an array")
		}
	}
	command := launcher
	var kept []any
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("SessionStart entry must be an object")
		}
		handlers, ok := entry["hooks"].([]any)
		if !ok {
			return nil, fmt.Errorf("SessionStart hooks must be an array")
		}
		remaining := make([]any, 0, len(handlers))
		for _, handler := range handlers {
			h, ok := handler.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("hook must be an object")
			}
			if h["statusMessage"] != hookOwner && h["statusMessage"] != legacyHookOwner {
				remaining = append(remaining, handler)
			}
		}
		if len(remaining) > 0 {
			entry["hooks"] = remaining
			kept = append(kept, entry)
		}
	}
	kept = append(kept, map[string]any{"matcher": "startup|resume|clear|compact", "hooks": []any{map[string]any{"type": "command", "command": command, "args": []string{"bootstrap", "--format", "claude-code"}, "statusMessage": hookOwner, "timeout": 5}}})
	hooks["SessionStart"] = kept
	doc["hooks"] = hooks
	return marshalConfig(doc)
}

// Replace only this server table and its child tables; all other TOML bytes survive.
// Refuse multiline strings rather than misidentifying a header inside one.
func mergeCodexServer(old []byte, name string, profile profiles.Profile) ([]byte, error) {
	text := string(old)
	if strings.Contains(text, `"""`) || strings.Contains(text, "'''") {
		return nil, fmt.Errorf("multiline TOML strings require manual Camp configuration; leaving file untouched")
	}
	target := "mcp_servers." + name
	lines := strings.SplitAfter(text, "\n")
	var out strings.Builder
	skipping := false
	section := ""
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if section == "mcp_servers" && !strings.HasPrefix(trim, "#") && !strings.HasPrefix(trim, "[") && strings.Contains(trim, "=") {
			return nil, fmt.Errorf("inline or dotted MCP server entries require manual configuration; leaving file untouched")
		}
		if strings.HasPrefix(trim, "mcp_servers") && strings.Contains(trim, "=") && !strings.HasPrefix(trim, "[") {
			return nil, fmt.Errorf("inline MCP configuration requires manual configuration")
		}
		if strings.HasPrefix(trim, "[") {
			end := strings.Index(trim, "]")
			if end < 0 {
				return nil, fmt.Errorf("malformed TOML table header")
			}
			header := strings.TrimSpace(trim[1:end])
			if strings.HasPrefix(trim, "[[") && strings.Contains(header, "mcp_servers") {
				return nil, fmt.Errorf("MCP array tables require manual configuration")
			}
			components := strings.Split(header, ".")
			for i := range components {
				components[i] = strings.TrimSpace(components[i])
			}
			header = strings.Join(components, ".")
			section = header
			// Quoted equivalent table names are ambiguous without a TOML parser.
			if strings.ContainsAny(header, "\"'") && strings.Contains(header, "mcp_servers") {
				return nil, fmt.Errorf("quoted MCP table names require manual Camp configuration")
			}
			skipping = header == target || strings.HasPrefix(header, target+".")
		}
		if !skipping {
			out.WriteString(line)
		}
	}
	result := out.String()
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	result += fmt.Sprintf("\n[%s]\ncommand = %q\nargs = [%q, %q, %q]\n", target, profile.Launcher, "bridge", "--profile", profile.Name)
	return []byte(result), nil
}
