// Package mcpserver hosts WeKnora's own MCP server surface: the Streamable
// HTTP endpoint that external MCP clients (Claude Desktop, Cursor, VS Code
// Copilot, ...) connect to. Every request is authenticated by
// middleware.MCPEndpointAuth against a workspace MCPEndpoint row, and the
// endpoint's tool allowlist and knowledge-base scope decide what the client
// can see and call.
//
// A single mcp-go server instance is registered with the full tool catalog;
// per-endpoint visibility is applied through a tool filter (tools/list) and a
// handler middleware (tools/call), both keyed off the endpoint on the request
// context. The transport runs stateless so any replica can answer any call.
package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/ratelimit"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	serverName    = "weknora"
	serverVersion = "1.0.0"

	rateLimitKeyPrefix = "mcp:endpoint:ratelimit:"
	lastUsedTouchEvery = time.Minute
)

var errNoEndpoint = errors.New("mcp endpoint missing from request context")

// Server wires the tool catalog onto an mcp-go server and exposes it as an
// http.Handler.
type Server struct {
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	chunkService     interfaces.ChunkService
	wikiService      interfaces.WikiPageService
	sessionService   interfaces.SessionService
	messageService   interfaces.MessageService
	agentService     interfaces.CustomAgentService
	kbShareService   interfaces.KBShareService
	tenantService    interfaces.TenantService
	endpointRepo     interfaces.MCPEndpointRepository
	db               *gorm.DB
	cfg              *config.Config

	limiter   *ratelimit.Limiter
	lastTouch sync.Map // endpoint id -> time.Time of the last last_used_at write

	mcp     *server.MCPServer
	handler http.Handler
}

// NewServer builds the MCP server and its Streamable HTTP transport.
func NewServer(
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	wikiService interfaces.WikiPageService,
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	agentService interfaces.CustomAgentService,
	kbShareService interfaces.KBShareService,
	tenantService interfaces.TenantService,
	endpointRepo interfaces.MCPEndpointRepository,
	db *gorm.DB,
	cfg *config.Config,
	redisClient *redis.Client,
) *Server {
	s := &Server{
		kbService:        kbService,
		knowledgeService: knowledgeService,
		chunkService:     chunkService,
		wikiService:      wikiService,
		sessionService:   sessionService,
		messageService:   messageService,
		agentService:     agentService,
		kbShareService:   kbShareService,
		tenantService:    tenantService,
		endpointRepo:     endpointRepo,
		db:               db,
		cfg:              cfg,
		limiter:          ratelimit.New(redisClient, rateLimitKeyPrefix, time.Minute, ""),
	}
	// The local fallback map only grows without periodic eviction; the server
	// lives for the whole process so the cleanup goroutine never stops.
	go s.limiter.StartCleanup(make(chan struct{}))
	s.mcp = server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithToolFilter(s.filterTools),
		server.WithToolHandlerMiddleware(s.guardTool),
		server.WithInstructions(serverInstructions),
	)
	s.mcp.AddTools(s.toolCatalog()...)
	s.handler = server.NewStreamableHTTPServer(
		s.mcp,
		server.WithStateLess(true),
	)
	return s
}

// Handler returns the http.Handler to mount under /mcp/:endpoint_id. The
// caller must run MCPEndpointAuth first; the handler trusts the context.
func (s *Server) Handler() http.Handler {
	return s.handler
}

const serverInstructions = "WeKnora knowledge workspace. Start with list_knowledge_bases to see what is in scope, " +
	"then use search_knowledge for semantic questions, grep_chunks for exact keywords, read_document to read " +
	"a whole document, and ask to get a synthesized answer with citations. Wiki tools browse the generated " +
	"wiki when a knowledge base has one. Write tools (add/update/delete_document) exist only on endpoints " +
	"that enabled them."

// endpointFromContext returns the endpoint authenticated for this call.
func endpointFromContext(ctx context.Context) (*types.MCPEndpoint, error) {
	ep, ok := ctx.Value(types.MCPEndpointContextKey).(*types.MCPEndpoint)
	if !ok || ep == nil {
		return nil, errNoEndpoint
	}
	return ep, nil
}

// filterTools hides catalog entries the endpoint did not enable.
func (s *Server) filterTools(ctx context.Context, all []mcp.Tool) []mcp.Tool {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return nil
	}
	out := make([]mcp.Tool, 0, len(all))
	for _, t := range all {
		if ep.HasTool(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// guardTool enforces the allowlist and rate limit on every tools/call, so a
// client cannot invoke a hidden tool by name.
func (s *Server) guardTool(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ep, err := endpointFromContext(ctx)
		if err != nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		if !ep.HasTool(req.Params.Name) {
			return mcp.NewToolResultErrorf("tool %q is not enabled on this endpoint", req.Params.Name), nil
		}
		if !s.limiter.Allow(ctx, ep.ID, ep.RateLimitPerMinute) {
			return mcp.NewToolResultError("rate limit exceeded for this endpoint, retry shortly"), nil
		}
		s.touchLastUsed(ctx, ep.ID)
		return next(ctx, req)
	}
}

// touchLastUsed records activity at most once a minute per endpoint.
func (s *Server) touchLastUsed(ctx context.Context, endpointID string) {
	if s.endpointRepo == nil {
		return
	}
	now := time.Now()
	if prev, ok := s.lastTouch.Load(endpointID); ok {
		if last, ok := prev.(time.Time); ok && now.Sub(last) < lastUsedTouchEvery {
			return
		}
	}
	s.lastTouch.Store(endpointID, now)
	go func() {
		bg, cancel := context.WithTimeout(logger.CloneContext(ctx), 5*time.Second)
		defer cancel()
		if err := s.endpointRepo.TouchLastUsed(bg, endpointID); err != nil {
			logger.Warnf(bg, "[mcpserver] touch last_used failed for %s: %v", endpointID, err)
		}
	}()
}

// toolCatalog lists every tool with its handler. Order matches the settings
// catalog so tools/list is stable.
func (s *Server) toolCatalog() []server.ServerTool {
	return []server.ServerTool{
		{Tool: listKnowledgeBasesTool(), Handler: s.handleListKnowledgeBases},
		{Tool: searchKnowledgeTool(), Handler: s.handleSearchKnowledge},
		{Tool: grepChunksTool(), Handler: s.handleGrepChunks},
		{Tool: listDocumentsTool(), Handler: s.handleListDocuments},
		{Tool: readDocumentTool(), Handler: s.handleReadDocument},
		{Tool: askTool(), Handler: s.handleAsk},
		{Tool: wikiSearchTool(), Handler: s.handleWikiSearch},
		{Tool: wikiReadPageTool(), Handler: s.handleWikiReadPage},
		{Tool: wikiIndexTool(), Handler: s.handleWikiIndex},
		{Tool: addDocumentTool(), Handler: s.handleAddDocument},
		{Tool: updateDocumentTool(), Handler: s.handleUpdateDocument},
		{Tool: deleteDocumentTool(), Handler: s.handleDeleteDocument},
	}
}

// toolResultFromAgentTool converts an agent tool result into an MCP result.
// Agent tools already format their Output for a model reader, so the text
// is passed through; Data is attached as structured content when present.
func toolResultFromAgentTool(res *types.ToolResult, err error) *mcp.CallToolResult {
	if err != nil && (res == nil || res.Error == "") {
		return mcp.NewToolResultError(err.Error())
	}
	if res == nil {
		return mcp.NewToolResultError("tool returned no result")
	}
	if !res.Success {
		msg := res.Error
		if msg == "" {
			msg = res.Output
		}
		if msg == "" {
			msg = "tool failed"
		}
		return mcp.NewToolResultError(msg)
	}
	if len(res.Data) > 0 {
		return mcp.NewToolResultStructured(res.Data, res.Output)
	}
	return mcp.NewToolResultText(res.Output)
}
