package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/denisqq/agent-camp/camp-server/camp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"os"
)

func (a *API) mcpServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "agent-camp", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: "At the start of meaningful work, consult the shared workspace; no project directory is required: map() without arguments, then read the space overview, rules, and recent decisions or handoffs — bounded, not the whole workspace. Start with map() without arguments to discover accessible spaces. Do not ask the user for internal space ids before discovery. Then map(space=id), start(primary_space=id), and run SDK examples from start.help. Read rules and document summaries before bodies. Check coverage; an incomplete tool search is not the full tool inventory. Writes require task authorization and remain subject to workspace permissions."})
	mcp.AddTool(server, &mcp.Tool{Name: "map", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, Description: "First entry: call map() with no arguments to list accessible spaces and root actions. No session or known space name needed. With space=id, explore that workspace before reading documents. Returns a bounded directory tree, record kinds, document title/description/status cards and link hubs; check coverage, then narrow path or use start/run to find and read relevant documents."}, func(ctx context.Context, req *mcp.CallToolRequest, in camp.MapRequest) (*mcp.CallToolResult, map[string]any, error) {
		p, err := a.mcpPrincipal(req)
		if err != nil {
			return nil, nil, err
		}
		out, err := a.Sessions.Broker.Map(ctx, p, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "start", Description: "Start a Agent Camp session in an authorized primary space. If its id is unknown, first call map() without arguments; do not ask the user to supply an internal id. Returns a small SDK guide; use help(topic) for details."}, func(ctx context.Context, req *mcp.CallToolRequest, in camp.StartRequest) (*mcp.CallToolResult, camp.SessionView, error) {
		p, err := a.mcpPrincipal(req)
		if err != nil {
			return nil, camp.SessionView{}, err
		}
		out, err := a.Sessions.Start(ctx, p, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "run", Description: "Requires a session from start. If the space is unknown, discover it with map() without arguments first. Run bounded Python using from camp_sdk import camp. Return a small JSON final expression. Successful staged writes commit together; retry the same operation_id only with identical code."}, func(ctx context.Context, req *mcp.CallToolRequest, in camp.RunRequest) (*mcp.CallToolResult, camp.Receipt, error) {
		p, err := a.mcpPrincipal(req)
		if err != nil {
			return nil, camp.Receipt{}, err
		}
		out, err := a.Sessions.Run(ctx, p, in)
		return nil, out, err
	})
	return server
}

func (a *API) mcp() http.Handler {
	server := a.mcpServer()
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
}

// RunStdio serves the same MCP server over standard io for clients that spawn
// the server as a process (Claude Desktop, most CLI agents).
func (a *API) RunStdio(ctx context.Context) error {
	token := os.Getenv("CAMP_TOKEN")
	if token == "" {
		return errors.New("CAMP_TOKEN is required for stdio MCP")
	}
	principal, err := a.Sessions.Broker.Auth.Authenticate(ctx, token)
	if err != nil {
		return fmt.Errorf("CAMP_TOKEN rejected: %w", err)
	}
	a.Fallback = func(context.Context) (camp.Principal, error) { return principal, nil }
	return a.mcpServer().Run(ctx, &mcp.StdioTransport{})
}
func (a *API) mcpPrincipal(req *mcp.CallToolRequest) (camp.Principal, error) {
	if (req.Extra == nil || req.Extra.TokenInfo == nil) && a.Fallback != nil {
		return a.Fallback(context.Background())
	}
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return camp.Principal{}, camp.ErrDenied
	}
	p, ok := req.Extra.TokenInfo.Extra["principal"].(camp.Principal)
	if !ok {
		return p, camp.ErrDenied
	}
	return p, nil
}
