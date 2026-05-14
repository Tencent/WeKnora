package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// Narrow per-domain service interfaces. ServiceClient (server.go) embeds
// them all; *sdk.Client satisfies the union implicitly.

type knowledgeBaseService interface {
	ListKnowledgeBases(ctx context.Context) ([]sdk.KnowledgeBase, error)
	GetKnowledgeBase(ctx context.Context, id string) (*sdk.KnowledgeBase, error)
}

type knowledgeService interface {
	ListKnowledgeWithFilter(ctx context.Context, kbID string, page, pageSize int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error)
	GetKnowledge(ctx context.Context, knowledgeID string) (*sdk.Knowledge, error)
	OpenKnowledgeFile(ctx context.Context, knowledgeID string) (string, io.ReadCloser, error)
	HybridSearch(ctx context.Context, kbID string, params *sdk.SearchParams) ([]*sdk.SearchResult, error)
}

type chatService interface {
	CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error)
	KnowledgeQAStream(ctx context.Context, sessionID string, req *sdk.KnowledgeQARequest, cb func(*sdk.StreamResponse) error) error
}

type agentService interface {
	ListAgents(ctx context.Context) ([]sdk.Agent, error)
	GetAgent(ctx context.Context, agentID string) (*sdk.Agent, error)
	AgentQAStreamWithRequest(ctx context.Context, sessionID string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error
}

// agentInvokeService composes the two SDK methods agent_invoke needs
// (CreateSession for the auto-session path + AgentQAStreamWithRequest
// for the run itself). Declared here alongside the per-domain
// interfaces above so ServiceClient (server.go) - which embeds the
// four domain interfaces - also satisfies it.
type agentInvokeService interface {
	CreateSession(ctx context.Context, req *sdk.CreateSessionRequest) (*sdk.Session, error)
	AgentQAStreamWithRequest(ctx context.Context, sessionID string, req *sdk.AgentQARequest, cb sdk.AgentEventCallback) error
}

// registerTools wires the curated 9 tools onto server. Adding a tool here
// is a deliberate API expansion - the agent-callable surface is the
// reason this CLI ships an MCP server, not its CLI command list, so this
// list must be maintained by hand.
func registerTools(server *mcpsdk.Server, svc ServiceClient) {
	addKBList(server, svc)
	addKBView(server, svc)
	addDocList(server, svc)
	addDocView(server, svc)
	addDocDownload(server, svc)
	addSearchChunks(server, svc)
	addChat(server, svc)
	addAgentList(server, svc)
	addAgentInvoke(server, svc)
}

// ---- kb_list -------------------------------------------------------------

type kbListInput struct{}

type kbListOutput struct {
	Items []sdk.KnowledgeBase `json:"items"`
}

func addKBList(server *mcpsdk.Server, svc knowledgeBaseService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "kb_list",
		Description: "List all knowledge bases visible to the active WeKnora tenant. No arguments. Returns items[]: each item carries id, name, description, knowledge_count, is_pinned, updated_at - useful for selecting a kb_id to pass to other tools.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ kbListInput) (*mcpsdk.CallToolResult, kbListOutput, error) {
		items, err := svc.ListKnowledgeBases(ctx)
		if err != nil {
			return nil, kbListOutput{}, fmt.Errorf("list knowledge bases: %w", err)
		}
		if items == nil {
			items = []sdk.KnowledgeBase{}
		}
		return nil, kbListOutput{Items: items}, nil
	})
}

// ---- kb_view -------------------------------------------------------------

type kbViewInput struct {
	KBID string `json:"kb_id" jsonschema:"knowledge base ID"`
}

func addKBView(server *mcpsdk.Server, svc knowledgeBaseService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "kb_view",
		Description: "Fetch a knowledge base by ID. Returns the full record including chunking config, embedding/summary model IDs, knowledge_count, and chunk_count.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in kbViewInput) (*mcpsdk.CallToolResult, *sdk.KnowledgeBase, error) {
		if in.KBID == "" {
			return nil, nil, fmt.Errorf("kb_id is required")
		}
		kb, err := svc.GetKnowledgeBase(ctx, in.KBID)
		if err != nil {
			return nil, nil, fmt.Errorf("get knowledge base: %w", err)
		}
		return nil, kb, nil
	})
}

// ---- doc_list ------------------------------------------------------------

type docListInput struct {
	KBID     string `json:"kb_id" jsonschema:"knowledge base ID"`
	Page     int    `json:"page,omitempty" jsonschema:"1-indexed page number; defaults to 1"`
	PageSize int    `json:"page_size,omitempty" jsonschema:"items per page (1..1000); defaults to 20"`
	Status   string `json:"status,omitempty" jsonschema:"filter by parse status: pending | processing | completed | failed"`
}

type docListOutput struct {
	Items    []sdk.Knowledge `json:"items"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
	Total    int64           `json:"total"`
}

func addDocList(server *mcpsdk.Server, svc knowledgeService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_list",
		Description: "List documents in a knowledge base, with pagination and optional parse-status filter. Returns items[] with id, file_name, title, parse_status, size, updated_at - plus the page/total metadata.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docListInput) (*mcpsdk.CallToolResult, docListOutput, error) {
		if in.KBID == "" {
			return nil, docListOutput{}, fmt.Errorf("kb_id is required")
		}
		page := in.Page
		if page < 1 {
			page = 1
		}
		size := in.PageSize
		if size < 1 {
			size = 20
		}
		if size > 1000 {
			return nil, docListOutput{}, fmt.Errorf("page_size must be in 1..1000")
		}
		items, total, err := svc.ListKnowledgeWithFilter(ctx, in.KBID, page, size,
			sdk.KnowledgeListFilter{ParseStatus: in.Status})
		if err != nil {
			return nil, docListOutput{}, fmt.Errorf("list documents: %w", err)
		}
		if items == nil {
			items = []sdk.Knowledge{}
		}
		return nil, docListOutput{Items: items, Page: page, PageSize: size, Total: total}, nil
	})
}

// ---- doc_view ------------------------------------------------------------

type docViewInput struct {
	KnowledgeID string `json:"knowledge_id" jsonschema:"document (knowledge entry) ID"`
}

func addDocView(server *mcpsdk.Server, svc knowledgeService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_view",
		Description: "Fetch a single document by ID. Returns the Knowledge record (file_name, title, type, parse_status, size, embedding_model_id, source URL if any, etc.).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docViewInput) (*mcpsdk.CallToolResult, *sdk.Knowledge, error) {
		if in.KnowledgeID == "" {
			return nil, nil, fmt.Errorf("knowledge_id is required")
		}
		k, err := svc.GetKnowledge(ctx, in.KnowledgeID)
		if err != nil {
			return nil, nil, fmt.Errorf("get knowledge: %w", err)
		}
		return nil, k, nil
	})
}

// ---- doc_download --------------------------------------------------------

type docDownloadInput struct {
	KnowledgeID string `json:"knowledge_id" jsonschema:"document (knowledge entry) ID"`
}

type docDownloadOutput struct {
	KnowledgeID string `json:"knowledge_id"`
	FileName    string `json:"file_name"`
	Bytes       int    `json:"bytes"`
	// Content is the file contents (UTF-8 if text, base64 if the SDK
	// reports a binary-looking blob). For binary, agents should decode
	// before consuming.
	Content  string `json:"content"`
	IsBase64 bool   `json:"is_base64"`
}

// maxDocDownloadBytes caps the per-call payload to keep an agent's context
// window safe; agents needing larger documents should chunk via doc_view +
// search_chunks. 1 MiB matches a typical LLM context-window budget for
// inline content (~250k tokens) while remaining cheap to serialize.
const maxDocDownloadBytes = 1 << 20

func addDocDownload(server *mcpsdk.Server, svc knowledgeService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_download",
		Description: "Download a document's raw bytes by ID. Capped at 1 MiB per call - for larger documents, use search_chunks to find the relevant excerpts. is_base64 reports whether content was base64-encoded (heuristic: presence of NUL byte in the first 512 bytes).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docDownloadInput) (*mcpsdk.CallToolResult, docDownloadOutput, error) {
		if in.KnowledgeID == "" {
			return nil, docDownloadOutput{}, fmt.Errorf("knowledge_id is required")
		}
		name, body, err := svc.OpenKnowledgeFile(ctx, in.KnowledgeID)
		if err != nil {
			return nil, docDownloadOutput{}, fmt.Errorf("open knowledge file: %w", err)
		}
		defer body.Close()
		buf, err := io.ReadAll(io.LimitReader(body, maxDocDownloadBytes+1))
		if err != nil {
			return nil, docDownloadOutput{}, fmt.Errorf("read knowledge file: %w", err)
		}
		if len(buf) > maxDocDownloadBytes {
			return nil, docDownloadOutput{}, fmt.Errorf("document exceeds the %d-byte per-call cap; use search_chunks for excerpts", maxDocDownloadBytes)
		}
		content, isBase64 := encodeDownload(buf)
		return nil, docDownloadOutput{
			KnowledgeID: in.KnowledgeID,
			FileName:    name,
			Bytes:       len(buf),
			Content:     content,
			IsBase64:    isBase64,
		}, nil
	})
}

// ---- search_chunks -------------------------------------------------------

type searchChunksInput struct {
	KBID             string  `json:"kb_id" jsonschema:"knowledge base ID to search"`
	Query            string  `json:"query" jsonschema:"natural-language search query"`
	Limit            int     `json:"limit,omitempty" jsonschema:"client-side cap on results (1..1000); defaults to 10"`
	VectorThreshold  float64 `json:"vector_threshold,omitempty" jsonschema:"minimum vector similarity (0..1)"`
	KeywordThreshold float64 `json:"keyword_threshold,omitempty" jsonschema:"minimum keyword score (0..1)"`
}

type searchChunksOutput struct {
	Results []*sdk.SearchResult `json:"results"`
}

func addSearchChunks(server *mcpsdk.Server, svc knowledgeService) {
	// Out = any: SDK output schema would derive from searchChunksOutput,
	// which embeds *sdk.SearchResult - and SearchResult.Metadata is a
	// nilable map[string]any that violates the auto-generated
	// type=object constraint when empty. Skipping derivation by using
	// `any` keeps the structured JSON shape identical while bypassing
	// the over-eager validator. Same pattern applied to chat / agent_invoke
	// below.
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "search_chunks",
		Description: "Hybrid (vector + keyword) retrieval against a knowledge base. Returns the top chunks ranked by RRF; use this before chat to ground an answer in cited context. Results include knowledge_id, content, score - feed back into chat as context or display directly.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchChunksInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return nil, nil, fmt.Errorf("kb_id is required")
		}
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query cannot be empty")
		}
		limit := in.Limit
		if limit < 1 {
			limit = 10
		}
		if limit > 1000 {
			return nil, nil, fmt.Errorf("limit must be in 1..1000")
		}
		results, err := svc.HybridSearch(ctx, in.KBID, &sdk.SearchParams{
			QueryText:        in.Query,
			VectorThreshold:  in.VectorThreshold,
			KeywordThreshold: in.KeywordThreshold,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("hybrid search: %w", err)
		}
		if len(results) > limit {
			results = results[:limit]
		}
		if results == nil {
			results = []*sdk.SearchResult{}
		}
		return nil, searchChunksOutput{Results: results}, nil
	})
}

// ---- chat ----------------------------------------------------------------

type chatInput struct {
	KBID      string `json:"kb_id" jsonschema:"knowledge base ID to chat against"`
	Query     string `json:"query" jsonschema:"user query"`
	SessionID string `json:"session_id,omitempty" jsonschema:"existing session to continue; auto-created when empty"`
}

type chatOutput struct {
	Answer             string              `json:"answer"`
	References         []*sdk.SearchResult `json:"references"`
	SessionID          string              `json:"session_id"`
	AssistantMessageID string              `json:"assistant_message_id,omitempty"`
}

func addChat(server *mcpsdk.Server, svc chatService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "chat",
		Description: "Stream a RAG answer from the LLM, grounded in the given knowledge base. The SSE stream is accumulated server-side; this tool returns the full answer + references + session_id once the stream completes. Pass session_id to continue a multi-turn conversation; otherwise a fresh session is auto-created.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in chatInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return nil, nil, fmt.Errorf("kb_id is required")
		}
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query cannot be empty")
		}
		sessionID := in.SessionID
		if sessionID == "" {
			sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora mcp chat"})
			if err != nil {
				return nil, nil, fmt.Errorf("create chat session: %w", err)
			}
			sessionID = sess.ID
		}
		req := &sdk.KnowledgeQARequest{
			Query:            in.Query,
			KnowledgeBaseIDs: []string{in.KBID},
			AgentEnabled:     false,
			Channel:          "api",
		}
		acc := &sse.Accumulator{}
		streamErr := svc.KnowledgeQAStream(ctx, sessionID, req, func(r *sdk.StreamResponse) error {
			acc.Append(r)
			return nil
		})
		if streamErr != nil {
			return nil, nil, fmt.Errorf("knowledge qa stream: %w", streamErr)
		}
		if !acc.Done() {
			return nil, nil, fmt.Errorf("stream ended without a terminal event")
		}
		sid := acc.SessionID
		if sid == "" {
			sid = sessionID
		}
		return nil, chatOutput{
			Answer:             acc.Result(),
			References:         acc.References,
			SessionID:          sid,
			AssistantMessageID: acc.AssistantMessageID,
		}, nil
	})
}

// ---- agent_list ----------------------------------------------------------

type agentListInput struct{}

type agentListOutput struct {
	Items []sdk.Agent `json:"items"`
}

func addAgentList(server *mcpsdk.Server, svc agentService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "agent_list",
		Description: "List the tenant's custom agents. Returns items[] with id, name, description, is_builtin - use to discover an agent_id before agent_invoke.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ agentListInput) (*mcpsdk.CallToolResult, agentListOutput, error) {
		items, err := svc.ListAgents(ctx)
		if err != nil {
			return nil, agentListOutput{}, fmt.Errorf("list agents: %w", err)
		}
		if items == nil {
			items = []sdk.Agent{}
		}
		return nil, agentListOutput{Items: items}, nil
	})
}

// ---- agent_invoke --------------------------------------------------------

type agentInvokeInput struct {
	AgentID   string `json:"agent_id" jsonschema:"custom agent ID"`
	Query     string `json:"query" jsonschema:"user query"`
	SessionID string `json:"session_id,omitempty" jsonschema:"existing session to continue; auto-created when empty"`
}

type agentInvokeOutput struct {
	Answer     string               `json:"answer"`
	References []*sdk.SearchResult  `json:"references"`
	ToolEvents []sse.AgentToolEvent `json:"tool_events,omitempty"`
	Thinking   string               `json:"thinking,omitempty"`
	SessionID  string               `json:"session_id"`
	AgentID    string               `json:"agent_id"`
}

func addAgentInvoke(server *mcpsdk.Server, svc agentInvokeService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "agent_invoke",
		Description: "Run a query through a custom agent (system prompt + tool allow-list + KB scope). The agent's SSE stream is accumulated server-side; this tool returns the final answer plus the trace (references, tool_events, thinking).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in agentInvokeInput) (*mcpsdk.CallToolResult, any, error) {
		if in.AgentID == "" {
			return nil, nil, fmt.Errorf("agent_id is required")
		}
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query cannot be empty")
		}
		acc := &sse.AgentAccumulator{}
		req := &sdk.AgentQARequest{
			Query:        in.Query,
			AgentEnabled: true,
			AgentID:      in.AgentID,
			Channel:      "api",
		}
		// Auto-create session if not supplied. Sessions are agent-
		// agnostic at creation (Q3 - verified against server source).
		sessionID := in.SessionID
		if sessionID == "" {
			sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora mcp agent_invoke"})
			if err != nil {
				return nil, nil, fmt.Errorf("create chat session: %w", err)
			}
			sessionID = sess.ID
		}
		streamErr := svc.AgentQAStreamWithRequest(ctx, sessionID, req, func(r *sdk.AgentStreamResponse) error {
			acc.Append(r)
			return nil
		})
		if streamErr != nil {
			return nil, nil, fmt.Errorf("agent-chat stream: %w", streamErr)
		}
		if !acc.Done() {
			return nil, nil, fmt.Errorf("stream ended without a terminal event")
		}
		return nil, agentInvokeOutput{
			Answer:     acc.Answer(),
			References: acc.References,
			ToolEvents: acc.ToolEvents,
			Thinking:   acc.Thinking(),
			SessionID:  sessionID,
			AgentID:    in.AgentID,
		}, nil
	})
}

// encodeDownload returns (content, isBase64). Heuristic: if the first 512
// bytes contain a NUL, treat as binary. Otherwise it's UTF-8-ish text.
// Matches what /usr/bin/file's "binary" heuristic does at a coarse level -
// good enough to spare an agent from base64-decoding obvious text.
func encodeDownload(buf []byte) (string, bool) {
	probe := buf
	if len(probe) > 512 {
		probe = probe[:512]
	}
	for _, b := range probe {
		if b == 0 {
			return base64.StdEncoding.EncodeToString(buf), true
		}
	}
	return string(buf), false
}
