package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// ToolHandler runs one tool call. args are the arguments as the model sent
// them; decode them into the shape of the tool's input schema. Return
// pluginapi.ToolError (or any error) for failures the model should see.
type ToolHandler func(ctx context.Context, call *Call, args json.RawMessage) (*pluginapi.ToolResult, error)

type mcpServer struct {
	tools    []pluginapi.Tool
	handlers map[string]ToolHandler
}

// Tool adds a tool to the MCP server the plugin serves as server
// (contributes.mcpServers[].id, declared without a url). Agents in the
// workspaces that switched the plugin on can call it like any MCP tool,
// with the same approval settings; call carries the workspace's
// configuration.
func (p *Plugin) Tool(server string, tool pluginapi.Tool, h ToolHandler) {
	s := p.mcp[server]
	if s == nil {
		s = &mcpServer{handlers: map[string]ToolHandler{}}
		p.mcp[server] = s
	}
	if len(tool.InputSchema) == 0 {
		tool.InputSchema = json.RawMessage(`{"type":"object"}`)
	}
	if _, dup := s.handlers[tool.Name]; !dup {
		s.tools = append(s.tools, tool)
	}
	s.handlers[tool.Name] = h
}

func (p *Plugin) routeMCP(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/mcp/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		s, ok := p.mcp[id]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no MCP server %q", id))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			req, err := decodeInput[pluginapi.MCPRequest](raw)
			if err != nil {
				return nil, err
			}
			return p.answerMCP(ctx, call, id, s, req), nil
		})(w, r)
	})
}

func (p *Plugin) answerMCP(
	ctx context.Context, call *Call, id string, s *mcpServer, req pluginapi.MCPRequest,
) pluginapi.MCPResponse {
	resp := pluginapi.MCPResponse{JSONRPC: "2.0", ID: req.ID}
	result := func(v any) pluginapi.MCPResponse {
		b, err := json.Marshal(v)
		if err != nil {
			resp.Error = &pluginapi.MCPError{Code: -32603, Message: err.Error()}
			return resp
		}
		resp.Result = b
		return resp
	}
	fail := func(code int, msg string) pluginapi.MCPResponse {
		resp.Error = &pluginapi.MCPError{Code: code, Message: msg}
		return resp
	}
	switch req.Method {
	case "initialize":
		return result(map[string]any{
			"protocolVersion": pluginapi.MCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": p.info.ID + "/" + id, "version": p.info.Version},
		})
	case "ping":
		return result(struct{}{})
	// Tools only: clients that list resources or prompts anyway get none.
	case "resources/list":
		return result(map[string]any{"resources": []any{}})
	case "resources/templates/list":
		return result(map[string]any{"resourceTemplates": []any{}})
	case "prompts/list":
		return result(map[string]any{"prompts": []any{}})
	case "tools/list":
		tools := append([]pluginapi.Tool(nil), s.tools...)
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		return result(map[string]any{"tools": tools})
	case "tools/call":
		var params pluginapi.ToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(pluginapi.MCPInvalidParams, "params: "+err.Error())
		}
		h, ok := s.handlers[params.Name]
		if !ok || h == nil {
			return fail(pluginapi.MCPInvalidParams, "no tool "+params.Name)
		}
		args := params.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage(`{}`)
		}
		out, err := h(ctx, call, args)
		if err != nil {
			msg := err.Error()
			if pe, ok := pluginapi.AsError(err); ok {
				msg = pe.Message
			}
			if errors.Is(err, context.Canceled) {
				msg = "the call was cancelled"
			}
			out = pluginapi.ToolError("%s", msg)
		}
		if out == nil {
			out = pluginapi.TextResult("")
		}
		if out.Content == nil {
			out.Content = []pluginapi.ToolContent{}
		}
		return result(out)
	default:
		if len(req.ID) == 0 {
			// Notifications need no answer; the server keeps no state.
			return resp
		}
		return fail(pluginapi.MCPMethodNotFound, "method "+req.Method+" is not supported")
	}
}
