package cli

import (
	"encoding/json"
	"fmt"

	"github.com/denisqq/agent-camp/internal/clientconfig"
	"github.com/spf13/cobra"
)

// camp connect installs the camp integration into a specific agent client:
// the MCP bridge entry plus the workflow skill. Client configs are merged,
// never overwritten: other MCP servers and unrelated settings survive, and
// each install replaces only Camp-owned entries. --print shows the config
// instead of writing it.
// connectCmd installs the Camp integration into one agent client.
func connectCmd() *cobra.Command {
	var url, profileName, name string
	var printOnly bool
	c := &cobra.Command{
		Use:   "connect <claude-code|claude-desktop|codex|cursor|generic>",
		Short: "Install the Camp MCP bridge and workflow skill into a client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connectRun(args[0], url, profileName, name, printOnly)
		},
	}
	c.Flags().StringVar(&url, "url", env("CAMP_MCP_URL", "http://127.0.0.1:8090"), "camp server URL")
	c.Flags().StringVar(&profileName, "profile", "local", "private profile name")
	c.Flags().StringVar(&name, "name", "agent-camp", "server name in client configs")
	c.Flags().BoolVar(&printOnly, "print", false, "print config instead of installing")
	return c
}

func connectRun(clientKey, url, profileName, name string, printOnly bool) error {
	launcher, err := absoluteLauncher()
	if err != nil {
		return err
	}
	profile := clientconfig.Profile{Name: profileName, ServerURL: url, CredentialRef: "profile-" + profileName, Launcher: launcher}

	if printOnly || clientKey == "generic" {
		entry := map[string]any{"mcpServers": map[string]any{
			name: map[string]any{"type": "stdio", "command": launcher, "args": []string{"bridge", "--profile", profile.Name}},
		}}
		b, _ := json.MarshalIndent(entry, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	client := clientconfig.Client{Key: clientKey}
	result, err := clientconfig.Install(client, clientconfig.Scope{}, profile, name)
	if err != nil {
		return err
	}
	for _, file := range result.Files {
		fmt.Printf("updated %s\n", file)
	}
	if result.Detail != "" {
		fmt.Println(result.Detail)
	}
	fmt.Printf("State: %s — tools are live only after the client reloads and connects.\n", result.State)
	fmt.Printf("The credential is read from private profile storage (%s); no CAMP_TOKEN export is needed.\n", profile.CredentialRef)
	return nil
}
