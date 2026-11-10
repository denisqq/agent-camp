package campcli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/denisqq/agent-camp/camp-cli/clients"
	"github.com/denisqq/agent-camp/camp-cli/guide"
	"github.com/denisqq/agent-camp/installer/profiles"
	"github.com/spf13/cobra"
)

// connectCmd installs client-wide integration by default; --project limits its scope.
func connectCmd() *cobra.Command {
	var url, profileName, name string
	var printOnly, project bool
	cmd := &cobra.Command{Use: "connect <claude-code|claude-desktop|codex|cursor|generic>", Short: "Connect Camp and install client-wide startup instructions", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return connectWithScope(cmd.OutOrStdout(), args[0], url, profileName, name, printOnly, project)
	}}
	cmd.Flags().StringVar(&url, "url", env("CAMP_MCP_URL", "http://127.0.0.1:8090"), "camp server URL")
	cmd.Flags().StringVar(&profileName, "profile", "local", "private profile name")
	cmd.Flags().StringVar(&name, "name", "agent-camp", "server name in client configs")
	cmd.Flags().BoolVar(&printOnly, "print", false, "preview Camp-owned changes without writing or exposing existing settings")
	cmd.Flags().BoolVar(&project, "project", false, "limit integration and startup instructions to the current directory")
	return cmd
}

func connectRun(clientKey, url, profileName, name string, printOnly bool) error {
	return connectWithScope(os.Stdout, clientKey, url, profileName, name, printOnly, false)
}
func connectWithScope(out io.Writer, clientKey, url, profileName, name string, printOnly, project bool) error {
	launcher, err := absoluteLauncher()
	if err != nil {
		return err
	}
	if name == "" {
		name = "agent-camp"
	}
	profile := profiles.Profile{Name: profileName, ServerURL: url, CredentialRef: "profile-" + profileName, Launcher: launcher}
	if clientKey == "generic" {
		if err := profile.Validate(); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"mcpServers": map[string]any{name: map[string]any{"type": "stdio", "command": launcher, "args": []string{"bridge", "--profile", profile.Name}}}, "bootstrap_mode": "manual", "instructions": guide.Bootstrap})
	}
	scope := clients.Scope{Project: project}
	if project {
		scope.Root, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	client := clients.Client{Key: clientKey}
	if printOnly {
		files, result, err := clients.Preview(client, scope, profile, name)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(struct {
			Files  []clients.FileEdit    `json:"files"`
			Result clients.InstallResult `json:"result"`
		}{files, result})
	}
	result, err := clients.Install(client, scope, profile, name)
	if err != nil {
		return err
	}
	for _, file := range result.Files {
		fmt.Fprintf(out, "updated %s\n", file)
	}
	fmt.Fprintln(out, result.Detail)
	fmt.Fprintf(out, "Bootstrap: %s. State: %s — verify tools after the client reloads and connects.\n", result.BootstrapMode, result.State)
	fmt.Fprintf(out, "Credentials remain in private profile storage (%s).\n", profile.CredentialRef)
	return nil
}
