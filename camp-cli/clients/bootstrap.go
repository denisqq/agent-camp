package clients

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/denisqq/agent-camp/camp-cli/guide"
	"github.com/denisqq/agent-camp/installer/profiles"
)

const blockStart = "<!-- camp:bootstrap:start -->"
const blockEnd = "<!-- camp:bootstrap:end -->"
const hookOwner = "Camp workflow"
const legacyHookOwner = "Camp project workflow"

// FileEdit describes a destination and its Camp-owned additions in previews.
// Merge indicates that existing contents are preserved during installation.
type FileEdit struct {
	Path    string      `json:"path"`
	Content string      `json:"content"`
	Mode    os.FileMode `json:"-"`
	Merge   bool        `json:"merge"`
	before  []byte
	preview string
	root    string
}

type installation struct {
	files  []FileEdit
	result InstallResult
}

// Preview validates all target files and returns the edits without writing them.
func Preview(client Client, scope Scope, profile profiles.Profile, serverName string) ([]FileEdit, InstallResult, error) {
	plan, err := prepareInstall(client, scope, profile, serverName)
	if err != nil {
		return nil, InstallResult{}, err
	}
	// Each renderer also ran on empty input, so the preview contains only
	// Camp-owned additions and never private settings from the existing file.
	files := make([]FileEdit, 0, len(plan.files))
	for _, edit := range plan.files {
		edit.Content = edit.preview
		edit.before, edit.root, edit.preview = nil, "", ""
		files = append(files, edit)
	}
	return files, plan.result, nil
}

func defaultServerName(name string) string {
	if name == "" {
		return "agent-camp"
	}
	return name
}

func prepareInstall(client Client, scope Scope, profile profiles.Profile, name string) (installation, error) {
	var plan installation
	if err := profile.Validate(); err != nil {
		return plan, err
	}
	name = defaultServerName(name)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(name) {
		return plan, fmt.Errorf("invalid server name %q", name)
	}
	if strings.ContainsAny(profile.Launcher, "\r\n\x00") {
		return plan, fmt.Errorf("launcher contains control characters")
	}
	if client == ClaudeDesktop {
		result, err := installClaudeDesktop(profile, name)
		result.BootstrapMode = "manual"
		result.Detail += "\nAdd these client-wide instructions manually:\n" + guide.Bootstrap
		plan.result = result
		return plan, err
	}
	locations, err := installLocations(client, scope)
	if err != nil {
		return plan, err
	}
	if err = plan.addFile(locations.config, 0600, func(old []byte) ([]byte, error) {
		if client == Codex {
			return mergeCodexServer(old, name, profile)
		}
		doc, err := jsonObject(old)
		if err != nil {
			return nil, err
		}
		servers, err := objectField(doc, "mcpServers")
		if err != nil {
			return nil, err
		}
		servers[name] = bridgeServer(profile)
		doc["mcpServers"] = servers
		return marshalConfig(doc)
	}); err != nil {
		return installation{}, err
	}
	if err = plan.addFile(locations.skill, 0644, func([]byte) ([]byte, error) { return []byte(guide.Markdown), nil }); err != nil {
		return installation{}, err
	}
	mode := "hook"
	switch client {
	case Codex:
		target, err := effectiveInstructionTarget(locations.bootstrap)
		if err != nil {
			return installation{}, err
		}
		err = plan.addFile(target, 0644, func(old []byte) ([]byte, error) { return mergeManagedBlock(old, []byte(guide.Bootstrap)) })
		mode = "user-rule"
		if scope.Project {
			mode = "project-rule"
		}
		if err != nil {
			return installation{}, err
		}
	case ClaudeCode:
		if err = plan.addFile(locations.bootstrap, 0600, func(old []byte) ([]byte, error) { return mergeClaudeHook(old, profile.Launcher) }); err != nil {
			return installation{}, err
		}
	case Cursor:
		if scope.Project {
			if err = plan.addFile(locations.bootstrap, 0644, cursorProjectRule); err != nil {
				return installation{}, err
			}
			mode = "project-rule"
		} else {
			script := cursorBootstrapScript(profile.Launcher)
			if err = plan.addFile(locations.script, 0700, func(old []byte) ([]byte, error) {
				if len(old) > 0 && !strings.HasPrefix(string(old), cursorScriptMarker()) {
					return nil, fmt.Errorf("unowned Camp hook script exists; leaving it untouched")
				}
				return []byte(script), nil
			}); err != nil {
				return installation{}, err
			}
			scriptRoot, err := canonicalInstallRoot(locations.script.root)
			if err != nil {
				return installation{}, err
			}
			command := cursorHookCommand(filepath.Join(scriptRoot, locations.script.rel))
			if err = plan.addFile(locations.bootstrap, 0600, func(old []byte) ([]byte, error) { return mergeCursorHook(old, command) }); err != nil {
				return installation{}, err
			}
		}
	}
	detail := "Reload the client to load Camp and its startup instructions; no project directory is required. Generated files do not verify a live connection."
	if scope.Project {
		detail = "Reload the client and trust this project to load its Camp integration. Generated files do not verify a live connection."
	}
	plan.result = InstallResult{State: "pending-reload", BootstrapMode: mode, Detail: detail}
	for _, file := range plan.files {
		plan.result.Files = append(plan.result.Files, file.Path)
	}
	return plan, nil
}
