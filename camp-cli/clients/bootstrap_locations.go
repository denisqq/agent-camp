package clients

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/denisqq/agent-camp/camp-cli/guide"
)

type installTarget struct{ root, rel string }
type clientLocations struct{ config, skill, bootstrap, script installTarget }

func installLocations(client Client, scope Scope) (clientLocations, error) {
	var locations clientLocations
	if client != ClaudeCode && client != Codex && client != Cursor {
		return locations, fmt.Errorf("unknown client %q", client.Key)
	}
	root := scope.Root
	if scope.Project {
		if root == "" {
			return locations, fmt.Errorf("project scope requires a root directory")
		}
	} else {
		var err error
		root, err = os.UserHomeDir()
		if err != nil {
			return locations, err
		}
	}
	target := func(rel string) installTarget { return installTarget{root, filepath.FromSlash(rel)} }
	switch client {
	case ClaudeCode:
		locations.config = target(".mcp.json")
		locations.skill = target(".claude/skills/" + guide.Name + "/SKILL.md")
		locations.bootstrap = target(".claude/settings.json")
		if !scope.Project {
			locations.config = target(".claude.json")
			if custom := os.Getenv("CLAUDE_CONFIG_DIR"); custom != "" {
				if !filepath.IsAbs(custom) {
					return locations, fmt.Errorf("CLAUDE_CONFIG_DIR must be absolute")
				}
				locations.config = installTarget{custom, ".claude.json"}
				locations.skill = installTarget{custom, filepath.Join("skills", guide.Name, "SKILL.md")}
				locations.bootstrap = installTarget{custom, "settings.json"}
			}
		}
	case Codex:
		locations.config = target(".codex/config.toml")
		locations.skill = target(".agents/skills/" + guide.Name + "/SKILL.md")
		locations.bootstrap = target("AGENTS.md")
		if !scope.Project {
			locations.bootstrap = target(".codex/AGENTS.md")
			if custom := os.Getenv("CODEX_HOME"); custom != "" {
				if !filepath.IsAbs(custom) {
					return locations, fmt.Errorf("CODEX_HOME must be absolute")
				}
				locations.config = installTarget{custom, "config.toml"}
				locations.bootstrap = installTarget{custom, "AGENTS.md"}
			}
		}
	case Cursor:
		locations.config = target(".cursor/mcp.json")
		locations.skill = target(".cursor/skills/" + guide.Name + "/SKILL.md")
		locations.bootstrap = target(".cursor/rules/agent-camp.mdc")
		if !scope.Project {
			locations.bootstrap = target(".cursor/hooks.json")
			extension := ".sh"
			if runtime.GOOS == "windows" {
				extension = ".ps1"
			}
			locations.script = target(".cursor/hooks/agent-camp-bootstrap" + extension)
		}
	}
	return locations, nil
}

func effectiveInstructionTarget(target installTarget) (installTarget, error) {
	root, err := canonicalInstallRoot(target.root)
	if err != nil {
		return target, err
	}
	override := filepath.Join(filepath.Dir(target.rel), "AGENTS.override.md")
	path := filepath.Join(root, override)
	if err = safeInstallPath(root, path); err != nil {
		return target, err
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return target, err
	}
	if strings.TrimSpace(string(body)) != "" {
		target.rel = override
	}
	return target, nil
}

// Resolve an explicitly selected root even if a custom client profile directory
// does not exist yet. Nested symlinks under the selected root remain forbidden.
func canonicalInstallRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if parent == absolute {
		return "", err
	}
	resolved, err = canonicalInstallRoot(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(absolute)), nil
}

func (p *installation) addFile(target installTarget, mode os.FileMode, render func([]byte) ([]byte, error)) error {
	root, err := canonicalInstallRoot(target.root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, target.rel)
	if err = safeInstallPath(root, path); err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	// This owned POSIX hook is launched directly by Cursor and needs owner execute.
	if filepath.Base(path) == "agent-camp-bootstrap.sh" {
		mode |= 0100
	}
	body, err := render(old)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	preview, err := render(nil)
	if err != nil {
		return fmt.Errorf("preview %s: %w", path, err)
	}
	merge := filepath.Base(path) != "SKILL.md" && !strings.HasPrefix(filepath.Base(path), "agent-camp-bootstrap.")
	p.files = append(p.files, FileEdit{Path: path, Content: string(body), Mode: mode, Merge: merge, before: old, preview: string(preview), root: root})
	return nil
}
