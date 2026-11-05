package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/denisqq/agent-camp/internal/clientconfig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// camp bridge is the stdio adapter clients spawn: it reads the credential
// from private profile storage (so GUI clients need no exported
// CAMP_TOKEN), connects to the profile's HTTP server, and forwards the
// server's MCP surface. Remote profiles never start or fall back to a
// local server. Stdio carries only MCP protocol — logs go to stderr.
func bridge(args []string) error {
	flags := flag.NewFlagSet("bridge", flag.ContinueOnError)
	profileName := flags.String("profile", "local", "private profile name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store := clientconfig.Store{Dir: clientconfig.DefaultStoreDir()}
	profile, err := store.LoadProfile(*profileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no camp profile %q; run camp register or camp setup first", *profileName)
		}
		return err
	}
	token := ""
	if profile.CredentialRef != "" {
		if token, err = store.LoadCredential(profile.CredentialRef); err != nil {
			return fmt.Errorf("credential for profile %q is unavailable: %w", *profileName, err)
		}
	}
	endpoint := strings.TrimSuffix(profile.ServerURL, "/") + "/mcp"
	client := mcp.NewClient(&mcp.Implementation{Name: "camp-bridge", Version: "0.1.0"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token}}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}, nil)
	if err != nil {
		return fmt.Errorf("camp server at %s is unreachable: %w", profile.ServerURL, err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("list tools from %s: %w", profile.ServerURL, err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "agent-camp-bridge", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: "Camp bridge: same tools and permissions as the configured server. Start with map() with no arguments to discover spaces."})
	for _, tool := range tools.Tools {
		tool := tool
		mcp.AddTool(server, &mcp.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Annotations: tool.Annotations,
			InputSchema: tool.InputSchema,
		}, func(ctx context.Context, req *mcp.CallToolRequest, in json.RawMessage) (*mcp.CallToolResult, json.RawMessage, error) {
			var arguments map[string]any
			if len(in) > 0 {
				if err := json.Unmarshal(in, &arguments); err != nil {
					return nil, nil, err
				}
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: arguments})
			if err != nil {
				return nil, nil, err
			}
			var structured json.RawMessage
			if raw, err := json.Marshal(result.StructuredContent); err == nil && result.StructuredContent != nil {
				structured = raw
			}
			out := &mcp.CallToolResult{Content: result.Content, IsError: result.IsError, StructuredContent: result.StructuredContent}
			return out, structured, nil
		})
	}
	fmt.Fprintf(os.Stderr, "camp bridge: connected to %s as profile %s\n", profile.ServerURL, profile.Name)
	return server.Run(ctx, &mcp.StdioTransport{})
}

type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// absoluteLauncher returns the absolute path of the running camp binary so
// client configs work without a shell PATH.
func absoluteLauncher() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}
