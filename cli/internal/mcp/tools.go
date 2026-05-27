package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// toolErrorResult builds an error CallToolResult with IsError=true, a
// human-readable text fallback (Content), and the error envelope payload
// (StructuredContent). Reuses cmdutil.ErrorToDetail so the hint / retry /
// risk / detail fallback table stays single-source.
//
// Returns nil when err is nil; callers should only invoke on a real error.
//
// CAVEAT (go-sdk v1.6.0): SetError(err) clobbers Content; we manually
// build CallToolResult instead.
func toolErrorResult(err error) *mcpsdk.CallToolResult {
	if err == nil {
		return nil
	}
	detail := cmdutil.ErrorToDetail(err)
	textLine := detail.Type + ": " + detail.Message
	if detail.Hint != "" {
		textLine += "\nhint: " + detail.Hint
	}
	if detail.RetryCommand != "" {
		textLine += "\nretry: " + detail.RetryCommand
	}
	// StructuredContent accepts any; pass *ErrDetail directly (no round-trip).
	return &mcpsdk.CallToolResult{
		IsError:           true,
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: textLine}},
		StructuredContent: detail,
	}
}

// successResult builds a CallToolResult with StructuredContent = payload.
// Using Out=any in all handlers disables the SDK auto-marshal path (which
// would overwrite our StructuredContent with a zero-struct when the handler
// returns a typed nil). We manually populate both StructuredContent and a
// text Content fallback so the shape is identical on success and error.
func successResult(payload any) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		StructuredContent: payload,
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: marshalToString(payload)}},
	}
}

func marshalToString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

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

// chunkListService is the narrow surface chunk_list depends on. Kept
// separate from knowledgeService because the chunk subtree is its own
// domain on the server side (/api/v1/chunks/...).
type chunkListService interface {
	ListKnowledgeChunks(ctx context.Context, knowledgeID string, page, pageSize int, chunkTypes ...string) ([]sdk.Chunk, int64, error)
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

// registerTools wires the curated 10 tools onto server. Adding a tool here
// is a deliberate API expansion - the agent-callable surface is the
// reason this CLI ships an MCP server, not its CLI command list, so this
// list must be maintained by hand.
//
// TODO(v0.8): add OutputSchema to each mcpsdk.Tool registration so agents
// can type-check responses without structural probing. Currently omitted
// because the Out type is `any` on all handlers (required to suppress the
// SDK's auto-marshal which clobbers our manually-populated StructuredContent).
// When go-sdk exposes a typed OutputSchema field independent of the handler
// Out type, populate it from the corresponding *Output struct.
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
	addChunkList(server, svc)
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
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ kbListInput) (*mcpsdk.CallToolResult, any, error) {
		items, err := svc.ListKnowledgeBases(ctx)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "list knowledge bases")), nil, nil
		}
		if items == nil {
			items = []sdk.KnowledgeBase{}
		}
		return successResult(kbListOutput{Items: items}), nil, nil
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
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in kbViewInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeKBIDRequired, "kb_id is required")), nil, nil
		}
		kb, err := svc.GetKnowledgeBase(ctx, in.KBID)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "get knowledge base")), nil, nil
		}
		return successResult(kb), nil, nil
	})
}

// ---- doc_list ------------------------------------------------------------

type docListInput struct {
	KBID      string `json:"kb_id" jsonschema:"knowledge base ID"`
	Page      int    `json:"page,omitempty" jsonschema:"1-indexed page number; defaults to 1"`
	PageSize  int    `json:"page_size,omitempty" jsonschema:"items per page (1..1000); defaults to 20"`
	Status    string `json:"status,omitempty" jsonschema:"filter by parse status: pending | processing | completed | failed"`
	Keyword   string `json:"keyword,omitempty" jsonschema:"server-side substring filter (case-sensitive LIKE against title / file_name); leave empty to skip"`
	FileType  string `json:"file_type,omitempty" jsonschema:"filter by file extension (e.g. pdf, md)"`
	Source    string `json:"source,omitempty" jsonschema:"filter by ingestion source (e.g. api, web)"`
	TagID     string `json:"tag_id,omitempty" jsonschema:"filter by tag association"`
	StartTime string `json:"start_time,omitempty" jsonschema:"include docs with updated_at >= this RFC3339 timestamp (e.g. 2006-01-02T15:04:05Z)"`
	EndTime   string `json:"end_time,omitempty" jsonschema:"include docs with updated_at <= this RFC3339 timestamp (e.g. 2006-01-02T15:04:05Z)"`
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
		Description: "List documents in a knowledge base, with pagination and optional filters (parse-status, keyword, file_type, source, tag_id, start_time/end_time on updated_at). Returns items[] with id, file_name, title, parse_status, size, updated_at - plus the page/total metadata.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docListInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeKBIDRequired, "kb_id is required")), nil, nil
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
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "page_size must be in 1..1000")), nil, nil
		}
		filter := sdk.KnowledgeListFilter{
			ParseStatus: in.Status,
			Keyword:     in.Keyword,
			FileType:    in.FileType,
			Source:      in.Source,
			TagID:       in.TagID,
		}
		if in.StartTime != "" {
			t, err := time.Parse(time.RFC3339, in.StartTime)
			if err != nil {
				return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("start_time must be RFC3339 (e.g. 2006-01-02T15:04:05Z), got %q", in.StartTime))), nil, nil
			}
			filter.StartTime = t
		}
		if in.EndTime != "" {
			t, err := time.Parse(time.RFC3339, in.EndTime)
			if err != nil {
				return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("end_time must be RFC3339 (e.g. 2006-01-02T15:04:05Z), got %q", in.EndTime))), nil, nil
			}
			filter.EndTime = t
		}
		items, total, err := svc.ListKnowledgeWithFilter(ctx, in.KBID, page, size, filter)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "list documents")), nil, nil
		}
		if items == nil {
			items = []sdk.Knowledge{}
		}
		return successResult(docListOutput{Items: items, Page: page, PageSize: size, Total: total}), nil, nil
	})
}

// ---- doc_view ------------------------------------------------------------

type docViewInput struct {
	DocID string `json:"doc_id" jsonschema:"document ID (same value as the doc-id positional in CLI commands)"`
}

func addDocView(server *mcpsdk.Server, svc knowledgeService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "doc_view",
		Description: "Fetch a single document by ID. Returns the Knowledge record (file_name, title, type, parse_status, size, embedding_model_id, source URL if any, etc.).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docViewInput) (*mcpsdk.CallToolResult, any, error) {
		if in.DocID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "doc_id is required")), nil, nil
		}
		k, err := svc.GetKnowledge(ctx, in.DocID)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "get knowledge")), nil, nil
		}
		return successResult(k), nil, nil
	})
}

// ---- doc_download --------------------------------------------------------

type docDownloadInput struct {
	DocID string `json:"doc_id" jsonschema:"document ID (same value as the doc-id positional in CLI commands)"`
}

type docDownloadOutput struct {
	DocID    string `json:"doc_id"`
	FileName string `json:"file_name"`
	Bytes    int    `json:"bytes"`
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
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in docDownloadInput) (*mcpsdk.CallToolResult, any, error) {
		if in.DocID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "doc_id is required")), nil, nil
		}
		name, body, err := svc.OpenKnowledgeFile(ctx, in.DocID)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "open knowledge file")), nil, nil
		}
		defer body.Close()
		buf, err := io.ReadAll(io.LimitReader(body, maxDocDownloadBytes+1))
		if err != nil {
			return toolErrorResult(cmdutil.Wrapf(cmdutil.CodeLocalFileIO, err, "read knowledge file")), nil, nil
		}
		if len(buf) > maxDocDownloadBytes {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputInvalidArgument, fmt.Sprintf("document exceeds the %d-byte per-call cap; use search_chunks for excerpts", maxDocDownloadBytes))), nil, nil
		}
		content, isBase64 := encodeDownload(buf)
		return successResult(docDownloadOutput{
			DocID:    in.DocID,
			FileName: name,
			Bytes:    len(buf),
			Content:  content,
			IsBase64: isBase64,
		}), nil, nil
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
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeKBIDRequired, "kb_id is required")), nil, nil
		}
		if strings.TrimSpace(in.Query) == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "query cannot be empty")), nil, nil
		}
		limit := in.Limit
		if limit < 1 {
			limit = 10
		}
		if limit > 1000 {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "limit must be in 1..1000")), nil, nil
		}
		results, err := svc.HybridSearch(ctx, in.KBID, &sdk.SearchParams{
			QueryText:        in.Query,
			MatchCount:       limit,
			VectorThreshold:  in.VectorThreshold,
			KeywordThreshold: in.KeywordThreshold,
		})
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "hybrid search")), nil, nil
		}
		if len(results) > limit {
			results = results[:limit]
		}
		if results == nil {
			results = []*sdk.SearchResult{}
		}
		return successResult(searchChunksOutput{Results: results}), nil, nil
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
	Thinking           string              `json:"thinking,omitempty"` // reasoning text from response_type=thinking; empty for non-reasoning models
	SessionID          string              `json:"session_id"`
	AssistantMessageID string              `json:"assistant_message_id,omitempty"`
	KBID               string              `json:"kb_id"`
	Query              string              `json:"query"`
}

func addChat(server *mcpsdk.Server, svc chatService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "chat",
		Description: "Stream a RAG answer from the LLM, grounded in the given knowledge base. The SSE stream is accumulated server-side (MCP tools/call has no standard partial-response, so this is NOT streaming); the tool returns the full accumulated response once the stream completes. Pass session_id to continue a multi-turn conversation; otherwise a fresh session is auto-created.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in chatInput) (*mcpsdk.CallToolResult, any, error) {
		if in.KBID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeKBIDRequired, "kb_id is required")), nil, nil
		}
		if strings.TrimSpace(in.Query) == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "query cannot be empty")), nil, nil
		}
		sessionID := in.SessionID
		if sessionID == "" {
			sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora mcp chat"})
			if err != nil {
				return toolErrorResult(cmdutil.WrapHTTP(err, "create chat session")), nil, nil
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
			return toolErrorResult(cmdutil.WrapHTTP(streamErr, "knowledge qa stream")), nil, nil
		}
		if !acc.Done() {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")), nil, nil
		}
		sid := acc.SessionID
		if sid == "" {
			sid = sessionID
		}
		return successResult(chatOutput{
			Answer:             acc.Result(),
			References:         acc.References,
			Thinking:           acc.Thinking(),
			SessionID:          sid,
			AssistantMessageID: acc.AssistantMessageID,
			KBID:               in.KBID,
			Query:              in.Query,
		}), nil, nil
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
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ agentListInput) (*mcpsdk.CallToolResult, any, error) {
		items, err := svc.ListAgents(ctx)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "list agents")), nil, nil
		}
		if items == nil {
			items = []sdk.Agent{}
		}
		return successResult(agentListOutput{Items: items}), nil, nil
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
	Query      string               `json:"query"`
}

func addAgentInvoke(server *mcpsdk.Server, svc agentInvokeService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "agent_invoke",
		Description: "Run a query through a custom agent (system prompt + tool allow-list + KB scope). The agent's SSE stream is accumulated server-side (MCP tools/call has no standard partial-response, so this is NOT streaming); the tool returns the final accumulated response once the stream completes.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in agentInvokeInput) (*mcpsdk.CallToolResult, any, error) {
		if in.AgentID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "agent_id is required")), nil, nil
		}
		if strings.TrimSpace(in.Query) == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "query cannot be empty")), nil, nil
		}
		acc := &sse.AgentAccumulator{}
		req := &sdk.AgentQARequest{
			Query:        in.Query,
			AgentEnabled: true,
			AgentID:      in.AgentID,
			Channel:      "api",
		}
		// Auto-create session if not supplied. Sessions are agent-
		// agnostic at creation (verified against server source).
		sessionID := in.SessionID
		if sessionID == "" {
			sess, err := svc.CreateSession(ctx, &sdk.CreateSessionRequest{Title: "weknora mcp agent_invoke"})
			if err != nil {
				return toolErrorResult(cmdutil.WrapHTTP(err, "create chat session")), nil, nil
			}
			sessionID = sess.ID
		}
		streamErr := svc.AgentQAStreamWithRequest(ctx, sessionID, req, func(r *sdk.AgentStreamResponse) error {
			acc.Append(r)
			return nil
		})
		if streamErr != nil {
			return toolErrorResult(cmdutil.WrapHTTP(streamErr, "agent-chat stream")), nil, nil
		}
		if !acc.Done() {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeSSEStreamAborted, "stream ended without a terminal event")), nil, nil
		}
		return successResult(agentInvokeOutput{
			Answer:     acc.Answer(),
			References: acc.References,
			ToolEvents: acc.ToolEvents,
			Thinking:   acc.Thinking(),
			SessionID:  sessionID,
			AgentID:    in.AgentID,
			Query:      in.Query,
		}), nil, nil
	})
}

// ---- chunk_list ----------------------------------------------------------

type chunkListInput struct {
	DocID string `json:"doc_id" jsonschema:"document (knowledge entry) ID"`
	Limit int    `json:"limit,omitempty" jsonschema:"max chunks to return (1..1000); defaults to 50"`
}

type chunkListOutput struct {
	Chunks           []sdk.Chunk `json:"chunks"`
	Total            int64       `json:"total"`
	TruncatedAtLimit bool        `json:"truncated_at_limit"`
}

// chunkListDefaultLimit + chunkListMaxLimit mirror the schema's default+max.
// MCP schema deliberately exposes only `limit`, not the CLI's full
// --limit/--page/--page-size triple: LLM agents typically need a single
// bounded fetch, not pagination workflows. Above 1000, fall back to the CLI.
const (
	chunkListDefaultLimit = 50
	chunkListMaxLimit     = 1000
)

func addChunkList(server *mcpsdk.Server, svc chunkListService) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "chunk_list",
		Description: "List chunks of a knowledge document for RAG retrieval debug. Returns at most `limit` chunks starting from ChunkIndex 0; if total chunks exceed limit, truncated_at_limit=true signals the agent to fall back to the CLI for paginated retrieval.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in chunkListInput) (*mcpsdk.CallToolResult, any, error) {
		if in.DocID == "" {
			return toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "doc_id is required")), nil, nil
		}
		// `limit` is typed as int by chunkListInput, so the SDK rejects
		// non-numeric values at schema validation (e.g. "limit":"50")
		// before this handler runs. Here we only default+clamp the
		// already-decoded value.
		limit := in.Limit
		if limit < 1 {
			limit = chunkListDefaultLimit
		}
		if limit > chunkListMaxLimit {
			limit = chunkListMaxLimit
		}
		chunks, total, err := svc.ListKnowledgeChunks(ctx, in.DocID, 1, limit)
		if err != nil {
			return toolErrorResult(cmdutil.WrapHTTP(err, "list knowledge chunks")), nil, nil
		}
		if chunks == nil {
			chunks = []sdk.Chunk{}
		}
		return successResult(chunkListOutput{
			Chunks:           chunks,
			Total:            total,
			TruncatedAtLimit: total > int64(limit),
		}), nil, nil
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
