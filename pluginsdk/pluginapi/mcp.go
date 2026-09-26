package pluginapi

import (
	"encoding/json"
	"fmt"
)

// MCPPath is the MCP server a plugin serves itself: an mcpServers
// contribution without a url. The input is one JSON-RPC 2.0 message of the
// Model Context Protocol, the output its JSON-RPC response. Carrying MCP in
// the envelope gives tool calls the workspace's context and configuration,
// and the same authentication as every other call. Servers are stateless:
// each message stands alone, with no session.
func MCPPath(id string) string { return "/v1/mcp/" + id }

// MCPProtocolVersion is the MCP revision plugins speak.
const MCPProtocolVersion = "2025-06-18"

// JSON-RPC error codes MCP servers answer with.
const (
	MCPMethodNotFound = -32601
	MCPInvalidParams  = -32602
)

// MCPRequest is a JSON-RPC request or notification (no ID).
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse is a JSON-RPC response.
type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// MCPError is a JSON-RPC error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool describes a tool for tools/list.
type Tool struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	// InputSchema is the JSON Schema of the arguments (type: object).
	InputSchema json.RawMessage `json:"inputSchema"`
	// OutputSchema describes StructuredContent, when the tool returns it.
	OutputSchema json.RawMessage  `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations are hints about a tool's behavior.
type ToolAnnotations struct {
	// ReadOnlyHint says the tool changes nothing.
	ReadOnlyHint bool `json:"readOnlyHint,omitempty"`
	// DestructiveHint says the tool may delete or overwrite.
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
}

// ToolCallParams are the params of tools/call.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolResult is what tools/call returns. The model reads Content;
// StructuredContent is data for the tool's result view (toolViews).
type ToolResult struct {
	Content           []ToolContent `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	// IsError marks a failure the model should see and may recover from.
	IsError bool `json:"isError,omitempty"`
}

// ToolContent is one content block: text, or an image (base64 Data).
type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// TextResult is a result the model reads as text.
func TextResult(text string) *ToolResult {
	return &ToolResult{Content: []ToolContent{{Type: "text", Text: text}}}
}

// StructuredResult returns data for the result view, and text for the model
// (the data as JSON when text is empty, as MCP recommends).
func StructuredResult(data any, text string) *ToolResult {
	if text == "" {
		b, err := json.Marshal(data)
		if err != nil {
			text = fmt.Sprint(data)
		} else {
			text = string(b)
		}
	}
	return &ToolResult{Content: []ToolContent{{Type: "text", Text: text}}, StructuredContent: data}
}

// ToolError is a failed call the model sees.
func ToolError(format string, args ...any) *ToolResult {
	r := TextResult(fmt.Sprintf(format, args...))
	r.IsError = true
	return r
}
