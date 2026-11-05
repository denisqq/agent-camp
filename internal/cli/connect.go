package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/denisqq/agent-camp/internal/clientconfig"
)

// camp connect installs the camp integration into a specific agent client:
// the MCP bridge entry plus the workflow skill. Client configs are merged,
// never overwritten: other MCP servers and unrelated settings survive, and
// each install replaces only Camp-owned entries. --print shows the config
// instead of writing it.
func connect(args []string) error {
	// Reorder so flags may come after the client name: camp connect codex --print.
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
	url := flags.String("url", env("CAMP_MCP_URL", "http://127.0.0.1:8090"), "camp server URL")
	profileName := flags.String("profile", "local", "private profile name")
	name := flags.String("name", "agent-camp", "server name in client configs")
	printOnly := flags.Bool("print", false, "print config instead of installing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: camp connect <claude-code|claude-desktop|codex|cursor|generic> [--print]")
	}

	launcher, err := os.Executable()
	if err != nil {
		return err
	}
	launcher, err = filepath.Abs(launcher)
	if err != nil {
		return err
	}
	profile := clientconfig.Profile{Name: *profileName, ServerURL: *url, CredentialRef: "profile-" + *profileName, Launcher: launcher}

	if *printOnly || flags.Arg(0) == "generic" {
		entry := map[string]any{"mcpServers": map[string]any{
			*name: map[string]any{"type": "stdio", "command": launcher, "args": []string{"bridge", "--profile", profile.Name}},
		}}
		b, _ := json.MarshalIndent(entry, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	client := clientconfig.Client{Key: flags.Arg(0)}
	result, err := clientconfig.Install(client, clientconfig.Scope{}, profile, *name)
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
