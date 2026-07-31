package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/dougkirkley/dross/pkg/provider"
)

// MCPServerSpec configures one MCP server the agent connects to. Its tools are
// exposed to the model namespaced as "<Name>__<tool>" so servers can't collide.
type MCPServerSpec struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // "http" (streamable) or "stdio"
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// mcpConn is a live MCP client connection, held for cleanup.
type mcpConn struct {
	name   string
	client *mcpclient.Client
}

const mcpToolSep = "__"

// WithMCPServers connects to each configured MCP server, lists its tools, and
// registers them as agent tools. A server that fails to connect is skipped (it
// must not break the agent). Call Agent.Close to release the connections.
func WithMCPServers(specs []MCPServerSpec) Option {
	return func(a *Agent) {
		for _, spec := range specs {
			if spec.Name == "" {
				continue
			}
			conn, defs, err := connectMCP(context.Background(), spec)
			if err != nil {
				a.emit("tool-result", fmt.Sprintf("[mcp %s unavailable: %v]", spec.Name, err))
				continue
			}
			a.mcpConns = append(a.mcpConns, conn)
			if a.extraTools == nil {
				a.extraTools = map[string]extraTool{}
			}
			for _, def := range defs {
				cli := conn.client
				remote := strings.TrimPrefix(def.Name, spec.Name+mcpToolSep)
				a.extraTools[def.Name] = extraTool{
					def: def,
					h: func(ctx context.Context, input json.RawMessage) (string, bool) {
						return callMCPTool(ctx, cli, remote, input)
					},
				}
			}
		}
	}
}

// connectMCP dials one MCP server, initializes the session, and returns its
// tools as namespaced ToolDefs.
func connectMCP(ctx context.Context, spec MCPServerSpec) (*mcpConn, []provider.ToolDef, error) {
	var (
		cli *mcpclient.Client
		err error
	)
	switch spec.Transport {
	case "stdio":
		if spec.Command == "" {
			return nil, nil, fmt.Errorf("stdio transport needs a command")
		}
		env := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			env = append(env, k+"="+v)
		}
		cli, err = mcpclient.NewStdioMCPClient(spec.Command, env, spec.Args...)
	default: // "http" / streamable HTTP
		if spec.URL == "" {
			return nil, nil, fmt.Errorf("http transport needs a url")
		}
		var opts []mcptransport.StreamableHTTPCOption
		if len(spec.Headers) > 0 {
			opts = append(opts, mcptransport.WithHTTPHeaders(spec.Headers))
		}
		cli, err = mcpclient.NewStreamableHttpClient(spec.URL, opts...)
	}
	if err != nil {
		return nil, nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := cli.Start(cctx); err != nil {
		cli.Close()
		return nil, nil, err
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "dross", Version: "1.0"}
	if _, err := cli.Initialize(cctx, initReq); err != nil {
		cli.Close()
		return nil, nil, err
	}
	lt, err := cli.ListTools(cctx, mcp.ListToolsRequest{})
	if err != nil {
		cli.Close()
		return nil, nil, err
	}
	defs := make([]provider.ToolDef, 0, len(lt.Tools))
	for _, t := range lt.Tools {
		props := t.InputSchema.Properties
		if props == nil {
			props = map[string]any{}
		}
		defs = append(defs, provider.ToolDef{
			Name:        spec.Name + mcpToolSep + t.Name,
			Description: t.Description,
			Properties:  props,
			Required:    t.InputSchema.Required,
		})
	}
	return &mcpConn{name: spec.Name, client: cli}, defs, nil
}

// ProbeMCP connects to a server, lists its tools, and disconnects — returning
// the tool count. Used by the TUI /mcp view to show live connection status.
func ProbeMCP(ctx context.Context, spec MCPServerSpec) (int, error) {
	conn, defs, err := connectMCP(ctx, spec)
	if err != nil {
		return 0, err
	}
	if conn != nil && conn.client != nil {
		_ = conn.client.Close()
	}
	return len(defs), nil
}

// callMCPTool invokes a tool on an MCP server and flattens its text content.
func callMCPTool(ctx context.Context, cli *mcpclient.Client, tool string, input json.RawMessage) (string, bool) {
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return err.Error(), true
		}
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := cli.CallTool(cctx, req)
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			b.WriteString(tc.Text)
			b.WriteByte('\n')
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "[no content]"
	}
	return out, res.IsError
}

// closeMCP releases all MCP connections (kills stdio processes, closes HTTP).
func (a *Agent) closeMCP() {
	for _, c := range a.mcpConns {
		if c.client != nil {
			_ = c.client.Close()
		}
	}
	a.mcpConns = nil
}
