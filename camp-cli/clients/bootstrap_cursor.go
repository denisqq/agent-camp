package clients

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/denisqq/agent-camp/camp-cli/guide"
)

func cursorProjectRule(old []byte) ([]byte, error) {
	if len(old) > 0 && !strings.Contains(string(old), blockStart) {
		return nil, fmt.Errorf("unowned rule exists; move it before installing Camp")
	}
	if len(old) == 0 {
		old = []byte("---\ndescription: Camp workflow\nalwaysApply: true\n---\n\n")
	}
	return mergeManagedBlock(old, []byte(guide.Bootstrap))
}

func cursorScriptMarker() string {
	if runtime.GOOS == "windows" {
		return "# Camp bootstrap: managed by camp connect\n"
	}
	return "#!/bin/sh\n# Camp bootstrap: managed by camp connect\n"
}
func cursorBootstrapScript(launcher string) string {
	if runtime.GOOS == "windows" {
		return cursorScriptMarker() + "& " + quotePowerShell(launcher) + " bootstrap --format cursor\nexit $LASTEXITCODE\n"
	}
	return cursorScriptMarker() + "exec " + quoteShell(launcher) + " bootstrap --format cursor\n"
}
func cursorHookCommand(script string) string {
	if runtime.GOOS == "windows" {
		return "& " + quotePowerShell(script)
	}
	return quoteShell(script)
}
func quoteShell(value string) string      { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func quotePowerShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func mergeCursorHook(old []byte, command string) ([]byte, error) {
	doc, err := jsonObject(old)
	if err != nil {
		return nil, err
	}
	if version, exists := doc["version"]; exists && version != float64(1) {
		return nil, fmt.Errorf("unsupported Cursor hooks version; leaving file untouched")
	}
	hooks, err := objectField(doc, "hooks")
	if err != nil {
		return nil, err
	}
	var entries []any
	if raw, exists := hooks["sessionStart"]; exists {
		var ok bool
		entries, ok = raw.([]any)
		if !ok {
			return nil, fmt.Errorf("sessionStart must be an array")
		}
	}
	kept := make([]any, 0, len(entries)+1)
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("sessionStart hook must be an object")
		}
		if entry["command"] != command {
			kept = append(kept, entry)
		}
	}
	kept = append(kept, map[string]any{"command": command, "timeout": 5})
	hooks["sessionStart"] = kept
	doc["version"], doc["hooks"] = 1, hooks
	return marshalConfig(doc)
}
