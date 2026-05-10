# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### 🚀 New Features
- **NEW**: `weknora` CLI v0.2 — the official command-line client lives under `cli/`. Mirrors the `gh` CLI `<noun> <verb>` convention with 10 top-level commands (`api`, `auth`, `chat`, `context`, `doc`, `doctor`, `kb`, `link`, `search`, `version`). Highlights:
  - Hybrid search and streaming RAG chat against any knowledge base.
  - Project-level binding via `weknora link` writing `.weknora/project.yaml` (vercel/netlify pattern); subcommands auto-resolve `--kb` from the link.
  - Stable JSON envelope (`{ok, data, error, _meta, dry_run, risk}`) on every `--json` invocation; closed error-code registry enforced by an AST scanner test.
  - Agent affordance: `--dry-run` for write commands, exit-code 10 + `input.confirmation_required` for non-interactive destructive writes, per-command "AI agents:" guidance auto-shown when CLAUDECODE / CURSOR_AGENT is set. Operational contract in `cli/AGENTS.md`.
  - Multi-context auth (`login` / `logout` / `list` / `status`), OS keyring + 0600 file fallback for credentials, both API-key and password (JWT) modes.
  - Health check via `weknora doctor` (4 statuses: ok / warn / fail / skip).
  - See `cli/README.md` for install + 5-minute quickstart.
- **NEW**: Adaptive 3-tier chunking — documents are now profiled before splitting and routed to one of three strategies: heading-aware (Markdown structure), heuristic (form-feeds, multilingual chapter markers DE/EN/ZH, all-caps titles, visual separators), or recursive (the modernized legacy splitter as a fallback). Auto-strategy is the new default for fresh KBs; existing KBs keep their previous behavior until the user opts in. See `docs/CHUNKING.md`.
- **NEW**: KB editor — chunking settings panel surfaces the new strategy selector (Automatic / Markdown-optimized / Smart structure detection / Classic) plus advanced options for token limit per chunk and language hints. Sharper inline help text on every setting explains when defaults apply and when to tune.
- **NEW**: Chunking debug panel — embedded "Test with sample text" panel under the chunking settings. Paste a snippet, hit Run preview, see selected tier, rejected tiers + reasons, document profile, size distribution stats over the full chunk set, and per-chunk cards with breadcrumb + content preview. Read-only, no DB or embedding side effects, 5-second server-side timeout.
- **NEW**: `POST /api/v1/chunker/preview` endpoint backing the debug panel. Returns `selected_tier`, `tier_chain`, `rejected[]`, `profile`, `chunks[]`, and `stats`. Capped at 64k input runes / 500 chunks per response.
- **NEW**: Per-tenant RRF (Reciprocal Rank Fusion) tuning — `RRFK`, `RRFVectorWeight`, `RRFKeywordWeight` are now configurable on the tenant `RetrievalConfig`. Defaults preserve the previous hardcoded behavior (k=60, weights 0.7/0.3).

### ⚡ Improvements
- **IMPROVED**: Chunker recursive priority — `splitBySeparators` now genuinely walks separators by priority and recursively re-splits oversize sub-pieces with the next-priority separator. Mirrors the Python reference. Without this fix, a "one paragraph break followed by a long run of newline-separated lines" pattern could emit ~1900-rune chunks at chunkSize=300.
- **IMPROVED**: ChunkOverlap default consolidated to 80 (~15% of ChunkSize). Previously the Go DefaultConfig used 64, the knowledge service used 50, the Python docreader used 100, and the frontend form initialised to 100. All paths now align.
- **IMPROVED**: ContextHeader (Markdown breadcrumb) lives on `Chunk.ContextHeader`, separate from `Chunk.Content`. Restores the `End-Start == len(Content)` invariant that the document-reconstruction path in `knowledge.go` relies on for summary generation and UI highlighting. Eliminates a duplicate-heading regression where the section heading appeared twice in a chunk's body.
- **IMPROVED**: Embedding pipeline — exponential backoff (200/400/800/1600/3200 ms) replaces the previous fixed 100ms × 5 retry loop, with context-cancellation between attempts. `sanitizeForEmbedding` caps single embedding inputs at 20k runes with a warning log on overflow.
- **IMPROVED**: SplitParentChild forces children onto the recursive tier, skipping per-parent profile passes (previously paid N extra O(N) document scans).
- **IMPROVED**: Heuristic splitter snaps overlap start to the nearest semantic boundary or newline instead of slicing mid-line / mid-word.
- **IMPROVED**: Validator flow — when every tier is rejected, the chain returns the legacy tier's output directly instead of running SplitText a second time.
- **IMPROVED**: Token limit per chunk — when set, ChunkSize is auto-clamped to a per-language character budget (with a 10% safety factor). Prevents overshooting embedding model token caps on CJK content where 1 char ≈ 0.6 tokens.
- **IMPROVED**: KB-config API — `strategy`, `tokenLimit`, `languages` use pointer DTOs server-side so a payload omitting a field means "no change" while an explicit empty / 0 / [] resets to default. Previously these were write-once fields.

### 🐛 Bug Fixes
- **FIXED**: Chunker — `Chunk.Start` / `End` rune-offset invariant restored after the heading-aware splitter started prepending breadcrumbs to content (regression introduced during the initial Tier-1 work, fixed before any release).
- **FIXED**: Heuristic splitter — `applyOverlap` aligns to boundaries instead of doing blind char-subtraction that could leave chunks starting mid-word in CJK text.
- **FIXED**: Preview endpoint — chunk-size statistics are now computed over the FULL chunk set before truncating the response payload to 500 entries. Previously `avg`/`min`/`max`/`stddev` reflected only the first 500 chunks of a larger split.
- **FIXED**: Preview endpoint — empty / whitespace-only sample text now returns a friendly 400 ("paste a sample…") instead of gin's cryptic `Field validation failed` error.
- **FIXED**: Frontend chunking debug panel — added explicit `type="button"`, prominent loading and error states, and console error logging so failed previews are debuggable from DevTools without enabling verbose logging. Earlier the panel could appear to "vanish" silently when a request failed.
- **FIXED**: KB-editor i18n — `chunkOverlap` initial form value aligned with the backend default (80, not 100); description texts on every chunking setting now state the recommended ranges per use-case.

### 📚 Documentation
- **DOC**: New `docs/CHUNKING.md` — strategy explanations, settings reference with use-case presets, token-limit guide per embedding model, debugging workflow, and known trade-offs.

## [0.5.1] - 2026-04-30

### 🚀 New Features
- **NEW**: WeChat Mini Program — added a lightweight mobile client (`miniprogram/`) for configuring WeKnora API access, selecting knowledge bases, importing URLs, and chatting from inside WeChat, extending WeKnora from desktop to mobile.
- **NEW**: Knowledge Base — document list view with multi-select, floating batch action bar, and batch delete to streamline managing large knowledge bases.
- **NEW**: IM — tenant-wide IM Channels Overview entry under the user menu so administrators can inspect every IM channel of the tenant from a single page.
- **NEW**: Sessions — keyword search across the conversation list, user-scoped pinning of important sessions, and clear IM-source visibility for chats originating from IM channels.
- **NEW**: Frontend — unified Model / Web Search / MCP settings pages onto a shared card + drawer pattern with consistent layouts and reusable confirm-delete behavior.
- **NEW**: IM channel form — switched from dialog to drawer UX, channel list moved from vertical layout to responsive grid cards with dropdown action menu, platform radio replaced with a select dropdown.
- **NEW**: Tenant — exposed API Key reset from the API Info page; create/reset returns plaintext key once.
- **NEW**: Storage — `STORAGE_ALLOW_LIST` env var to whitelist external storage hosts during URL rewriting / serving.
- **NEW**: Agent — configurable per-agent LLM call timeout from the agent editor frontend.
- **NEW**: Desktop client — added tenant switching support.
- **NEW**: Frontend — markdown test page under dev tools for previewing rendering behavior.

### ⚡ Improvements
- **IMPROVED**: Agent — `data_analysis` tool gained SQL validation and stricter type processing.
- **IMPROVED**: Agent — broad queries fall back to a knowledge-base document listing for better coverage (#959).
- **IMPROVED**: Wiki ingest — failed operations are now requeued, and sync task retry behavior is aligned across regular and Lite modes.
- **IMPROVED**: Wiki ingest (Lite) — added ingest lock to prevent concurrent execution issues.
- **IMPROVED**: Search — `RetrieverEngines.Scan` enhanced to support both legacy and current data formats.
- **IMPROVED**: i18n — aligned `en-US`, `ko-KR`, `ru-RU` locales with `zh-CN` as the source of truth.
- **IMPROVED**: Helm — preserve `SYSTEM_AES_KEY` / `TENANT_AES_KEY` across upgrades to avoid breaking existing encrypted data.
- **IMPROVED**: IM — secure private storage URL handling with HTTP rewriting in IM replies, presigned URL TTL shortening, and tenant ID preference from context.
- **IMPROVED**: Docs — README tracing references updated from Jaeger to Langfuse across all language variants; Agent Mode and Observability sections were polished.

### 🐛 Bug Fixes
- **FIXED**: Frontend — LaTeX formulas flashing and disappearing during streaming responses (#1056).
- **FIXED**: Docreader — removed default 100-page DOCX parsing limit.
- **FIXED**: IM — removed pipeline-level timeout that killed multi-round agent reasoning.
- **FIXED**: IM — sessions now isolated per agent and recover gracefully from deleted sessions.
- **FIXED**: Search — aligned rerank priority between `Execute` and `rerankResults`.
- **FIXED**: Container — aggregated registration errors in connector registry initialization for clearer startup diagnostics.
- **FIXED**: Crypto — fail loudly when encrypted DB fields cannot be decrypted instead of returning empty data.
- **FIXED**: Web search — normalized default tenant web search config at runtime.
- **FIXED**: Wiki ingest — silent data loss caused by malformed JSON in the Redis queue.
- **FIXED**: Tenant — return plaintext API key after create/reset for safe distribution.
- **FIXED**: Frontend — chat drag-and-drop uploads routed correctly to the right pipeline.
- **FIXED**: Frontend — hidden card checkbox when not in selection mode.
- **FIXED**: Frontend — knowledge list sticky header and floating batch bar polish.
- **FIXED**: Frontend — knowledge document list layout, batch bar, and selection behavior.
- **FIXED**: Frontend — restored hover feedback on selected list rows.
- **FIXED**: Frontend — keep chat input visible when conversation overflows the viewport.
- **FIXED**: Mini program — improved knowledge base selection flow.
- **FIXED**: Document parser — preserve standalone image uploads from the icon filter.
- **FIXED**: Knowledge — fixed attachment document failure handling.

### 🔧 Refactoring
- **REFACTOR**: IM — moved adapter factories into per-platform subpackages (`feishu/`, `wechat/`, `wecom/`, `slack/`, `telegram/`, `dingtalk/`, `mattermost/`) for cleaner package boundaries.

## [0.5.0] - 2026-04-27

### 🚀 New Features
- **NEW**: Wiki Mode — a brand-new agent-driven Wiki knowledge system that automatically distills raw documents into interlinked markdown pages. It ships with a dedicated WikiBrowser, an interactive knowledge graph visualizing references and relationships between pages, and specialized agent tools, empowering teams to grow a structured, continuously evolving knowledge base from their own materials.
- **NEW**: Observability — integrated Langfuse for agent ReAct loop, LLM token tracking, tool calls, and asynq pipeline tracing, providing deep visibility into agent reasoning, tool execution, and system performance.
- **NEW**: Customizable Indexing Strategy — users can now independently toggle Vector Search, Keyword Search, Wiki, and Knowledge Graph indexing on a per-knowledge-base level.
- **NEW**: Vector Store UI & Per-KB Binding — full frontend management interface for Vector Stores, allowing users to configure connections, test connectivity, and assign specific vector stores to different knowledge bases.
- **NEW**: Yuque Connector — Yuque data source integration with API client, full and incremental fetch, and resource mapping, enabling seamless synchronization of Yuque documents into the knowledge base.
- **NEW**: Built-in Agent Skills — added a preloaded `OpenMAIC Classroom` agent skill.
- **NEW**: Agent Tools — added `json_repair` tool for agents to automatically fix and parse malformed JSON outputs.
- **NEW**: Frontend — added copy action for model cards in settings.
- **NEW**: Agent — added support to load all sheets from Excel files for DuckDB data analysis.

### ⚡ Improvements
- **IMPROVED**: Agent — improved tenant context handling and error reporting.
- **IMPROVED**: Agent — updated synthesis and issue flagging instructions in system prompt.
- **IMPROVED**: Debugging — enhanced LLM request logging and debug output (`llm_debug`) across all model providers.

### 🐛 Bug Fixes
- **FIXED**: Agent — materialized knowledge files to temp path for DuckDB to fix access issues
- **FIXED**: Agent — removed rerank model requirement for wiki-only agents
- **FIXED**: Docreader — whitelisted offline protoc zip packages in dockerignore
- **FIXED**: System — changed hardcoded version to `*` comparison for new Linux version compatibility
- **FIXED**: Setup — added output if offline protoc install package already exists

## [0.4.0] - 2026-04-14

### 🚀 New Features
- **NEW**: Cloud Knowledge Assistant — [WeKnora Platform](https://weknora.weixin.qq.com/platform), a cloud-hosted knowledge assistant service for quick onboarding without local deployment
- **NEW**: WeKnora Cloud — WeKnora Cloud provider integration, providing hosted LLM models and document parsing capabilities, with credential management, status checks, and UI feedback
- **NEW**: Chrome Extension — browser extension support with menu entry and quick access integration for seamless knowledge capture from web pages
- **NEW**: WeChat IM Integration — WeChat channel adapter with QR code login and long-polling message support
- **NEW**: ClawHub Skill — WeKnora Skill published on ClawHub platform, enabling document import, hybrid search, and knowledge management via the WeKnora REST API
- **NEW**: Attachment Processing — file attachment support in chat pipeline with enhanced error handling, content formatting, and image/attachment metadata injection in queries
- **NEW**: Azure OpenAI Provider — full Azure OpenAI support for chat, VLM, and embedding models with deployment name preservation, configurable dimensions parameter, provider registration with metadata, URL auto-detection, and frontend provider integration with i18n
- **NEW**: Alibaba Cloud OSS Storage — object storage support via S3-compatible mode with configuration UI, connectivity test, status reporting, OSS TypeScript types, docreader OssStorage class, factory and container registration, and multi-language i18n (Korean, Russian)
- **NEW**: Notion Connector — Notion data source integration with API client, type definitions, Connector interface, markdown renderer, and dependency injection registration
- **NEW**: Baidu Web Search Provider — added Baidu as a web search provider option (#907)
- **NEW**: Ollama Web Search Provider — added Ollama as a web search provider option (#907)
- **NEW**: VectorStore Management — VectorStore entity, repository, database migrations, service layer with connection testing, and full CRUD API endpoints with Swagger documentation

### ⚡ Improvements
- Data source resource selector upgraded to tree view with parent-child indentation and cascading check/uncheck
- Customizable LLM call timeout for agents with docker-compose environment mapping
- Enhanced document summary with expandable/collapsible functionality and overflow detection for improved user interaction
- Improved chat UI with hover effects, database schema updates for chat history and retrieval configurations
- Enhanced chat pipeline query handling with image and attachment metadata
- Support custom endpoints for private WeCom deployments in IM channels
- Integrated Lumberjack for log file management with rotation and compression
- New document analysis prompt template and enhanced rewrite template descriptions
- Integrated ChatBot provider and docreader for unified chat service

### 🐛 Bug Fixes
- Fixed hardcoded TruncatePromptTokens in BatchEmbed causing unintended embedding truncation
- Fixed `<kb>` and `<web>` citation tags not being stripped before sending to IM platforms, causing raw tags in user-visible messages
- Fixed tool name duplication in streaming tool calls
- Fixed MINIO_ENDPOINT not configurable via environment variable
- Fixed Azure OpenAI dimensions support not gated properly for non-supporting models
- Fixed Azure OpenAI ModelMapperFunc overriding deployment names instead of preserving them as-is
- Fixed Azure OpenAI connection test not passing provider, causing incorrect endpoint usage
- Fixed 400 errors incorrectly treated as connection failures in model connectivity check (parameter mismatch is not a connectivity issue)
- Fixed Dockerfile build error with duplicate libsqlite3-0 and ffmpeg installation
- Fixed OSS S3-compatible API signature mismatch by disabling automatic checksum calculation and adjusting path-style settings
- Fixed missing closing brace in checkOSS function
- Fixed neo4j driver compatibility with Go 1.24 on Windows: reverted to v6 with -p=1 compiler workaround

### 🔧 Refactoring
- Replaced CryptoService with lightweight utils AES helpers, simplifying encryption logic across the codebase
- Optimized OSS storage initialization, URL formatting, and security handling for improved S3 compatibility
- Enhanced WeKnora Cloud internationalization and UI feedback for credential management operations

### 📚 Documentation
- Added VectorStore CRUD API endpoint documentation with Swagger annotations
- Added Alibaba Cloud OSS support documentation and API descriptions

## [0.3.6] - 2026-04-03

### 🚀 New Features
- **NEW**: ASR (Automatic Speech Recognition) — integrated ASR model support with audio file upload, in-document audio preview, and transcription capabilities; added ASR model connectivity check endpoint
- **NEW**: Data Source Auto-Sync (Feishu) — complete data source management with CRUD operations, Feishu Wiki/Drive auto-sync (incremental and full), sync logs with polling, tenant isolation, and data source type icons
- **NEW**: OIDC Authentication — OpenID Connect (OIDC) login support with auto-discovery, custom endpoint configuration, and user info field mapping
- **NEW**: IM Quote/Reply Context — extract quoted messages in IM channels (WeCom) and inject QuotedContext into LLM prompts for contextual replies; anti-hallucination handling for non-text quotes and unprocessable media
- **NEW**: Thread-Based IM Sessions — per-thread session mode for IM channels (Slack, Mattermost, Feishu, Telegram), enabling multi-user collaboration within message threads
- **NEW**: Document Summarization — AI-generated document summaries with configurable max_input_chars, dedicated summary section in document detail view with loading states
- **NEW**: Tavily Web Search Provider — added Tavily as a web search provider option; refactored web search provider architecture for extensibility
- **NEW**: MCP Auto-Reconnection — automatic reconnection logic for MCP tool calls and tool listing when server connection is lost
- **NEW**: Parallel Tool Calling — concurrent execution of multiple tool calls in agent mode via errgroup when ParallelToolCalls is enabled; sequential execution remains default
- **NEW**: Agent @Mention Scope Restriction — restrict user @mentions to agent's allowed knowledge base scope, preventing unauthorized access to knowledge bases and knowledge entries

### ⚡ Improvements
- Refined parent-child chunk replacement logic to only apply to text chunks whose parent is a parent_text chunk
- Optimized login page rendering performance: removed all backdrop-filter blur, reduced animated elements, added GPU compositing hints and prefers-reduced-motion support
- Unified NVIDIA API for both chat and VLM model types
- Prompt language fallback now uses WEKNORA_LANGUAGE environment variable instead of hardcoded zh-CN, with language propagated through document and image processing pipelines
- Fixed enable_thinking for Aliyun Qwen models in streaming mode
- Enhanced document processing with metadata extraction and handling
- Added header tracking for Markdown tables during chunking to preserve table context
- Elasticsearch ID field handling with dynamic .keyword suffix detection based on index mapping
- Added DOCREADER_DOCX_MAX_PAGES environment variable to limit DOCX parsing for large documents
- Knowledge tag batch update now includes authorization checks with agent-scoped KB access validation
- System proxy support for remote API calls
- DatabaseQueryTool enhanced with search scope filtering

### 🐛 Bug Fixes
- Fixed WeCom group chat @mention not being stripped from message text, causing all slash commands to fail
- Fixed SSEReader returning errors.New("EOF") instead of io.EOF, causing silent stream termination without done response
- Fixed extracted images not being deleted from storage when knowledge is removed, preventing orphan file accumulation
- Fixed S3 provider scheme not recognized in frontend/backend allowlists; added auto path-style addressing for non-AWS S3-compatible endpoints
- Fixed remote images in markdown files not being resolved during file upload (only base64/inline were handled)
- Fixed SSRF validation lacking IPv6 support; added IPv6 address and CIDR handling in whitelist mechanism
- Fixed web_fetch using removed IsSSRFSafeURL function; replaced with ValidateURLForSSRF
- Fixed mermaid diagrams not rendering on page refresh
- Fixed doc-content.vue renderer incompatible with marked v5+ token API
- Fixed null reference error when rendering empty markdown code blocks
- Fixed frontend using legacy storage_config instead of storage_provider_config, causing incorrect storage provider display
- Fixed knowledge document category not deselectable by clicking again
- Fixed duplicate click binding in frontend components
- Fixed migration numbering errors and removed broken update_updated_at_column trigger
- Fixed monkey patch for docx parse error handling

### 📚 Documentation
- Enhanced agent and knowledge base API documentation
- Added data source import documentation with architecture overview and quick start guide
- Updated README files with streamlined sections and feature overview across all languages
- Updated architecture diagram

### 🔧 Refactoring
- Improved question generation prompt template with better guidelines and context handling
- Simplified temperature option handling in chat request builders

## [0.3.5] - 2026-03-27

### 🚀 New Features
- **NEW**: Telegram IM Integration — Telegram bot adapter with webhook and long-polling modes, streaming replies via editMessageText, file download via getFile API, and timing-safe secret token verification
- **NEW**: DingTalk IM Integration — DingTalk bot supporting webhook (HmacSHA256 signature verification) and Stream mode (via dingtalk-stream-sdk-go), with AI Card streaming via OpenAPI and AccessToken caching
- **NEW**: Mattermost IM Channel — Mattermost IM channel adapter support
- **NEW**: IM Slash Command System — pluggable command framework with five built-in commands: /help, /info, /search, /stop, /clear; wired into all IM channel message dispatch
- **NEW**: IM Distributed Coordination — Redis-based multi-instance coordination: per-user queue limits, global concurrency gate, message dedup, WebSocket leader election, /stop cancellation for queued and in-flight requests
- **NEW**: Suggested Questions — agent-specific suggested questions API based on knowledge bases, with frontend display in chat and create-chat views; image knowledge auto-enqueues question generation tasks
- **NEW**: VLM Auto-Describe MCP Tool Images — when MCP tools return image content, the agent automatically generates text descriptions via the configured VLM model, making image data accessible to text-only LLMs
- **NEW**: Novita AI Provider — new LLM provider with OpenAI-compatible API supporting chat, embedding, and VLLM model types
- **NEW**: Channel Tracking — channel field added to knowledge entries and messages to track source (web/api/im/browser_extension) with frontend labels and DB migrations
- **NEW**: Expose Built-in Parser Engine in Settings — built-in parser engine now visible and selectable in the settings UI

### ⚡ Improvements
- MCP tool names now derived from service.Name (stable across server reconnections) instead of UUID; added collision detection and unique (tenant_id, name) DB index
- Frontend formats MCP tool names from snake_case (e.g. mcp_my_server_search_docs) to human-readable form (My Server Search Docs)
- Enhanced intent classification and context templates: runtime metadata (current time, weekday) injected into context, critical instructions added to rewrite template for entity/keyword preservation
- Knowledge search: added SQL LIKE wildcard escaping, title-based filtering, URL and HTML file type support; FindByMetadataKey method added
- Chunk search returns total chunk counts per knowledge ID for improved agent context awareness
- MiniMax models upgraded from M2.1/M2.1-lightning to M2.7/M2.7-highspeed; Novita AI MiniMax reference updated to M2.7
- DingTalk AI Card streaming: create/deliver/update via OpenAPI; shared think-block rendering via im.TransformThinkBlocks applied to all IM reply paths (DingTalk, Telegram, Feishu)
- IM stream orphan reaper and edit throttling added for DingTalk and Telegram; Feishu stream reaper fixes memory leak
- WeCom group chat replies fixed via appchat API with user fallback; empty-stream fallback when no visible content is produced
- Improved LLM call log summarization: limits output to last few messages to reduce verbosity
- ParallelToolCalls option added to ChatOptions

### 🐛 Bug Fixes
- Fixed agent producing empty response when no knowledge base is configured: retry (max 2), nudge message, and fallback response added
- Fixed UTF-8 byte-based truncation in summary fallback causing PostgreSQL invalid byte sequence errors for Chinese/emoji content; changed to rune-based truncation
- Fixed marked.js usage errors; upgraded marked dependency to v17.0.5 for correct code block rendering
- Fixed vLLM streaming: reasoning content now parsed and propagated through streaming pipeline alongside standard response
- Fixed frontend page counter not resetting to 1 after knowledge file operations (tag, upload, move, edit, delete), causing pagination skips
- Fixed image markdown being stripped during message sanitization
- Fixed MCP tool naming to use service.Name instead of UUID, preventing tool call failures after server reconnection
- Fixed global default storage engine not respected when creating a new knowledge base (was hardcoded to "local")
- Fixed API key encryption loss when updating tenant settings via PUT /tenants/kv/{key}: AfterFind-decrypted plaintext no longer written back to DB
- Fixed empty passage filtering in rerank to prevent Aliyun and Baidu Qianfan 400 errors
- Fixed markdown table rows being passed raw to rerank; now converted to plain text (col1, col2) before reranking
- Fixed OpenRouter embedding provider missing support
- Fixed Milvus vector metric type now configurable via MILVUS_METRIC_TYPE environment variable
- Fixed temperature validation to accept zero as a valid value (was previously defaulting)
- Fixed pg_search update guarded with skip_embedding to prevent unnecessary re-embedding
- Fixed thinking block content being indexed into chat history knowledge base, degrading RAG retrieval quality

### 📚 Documentation
- Added Telegram and DingTalk IM platform setup guides (WebSocket/Webhook modes, streaming, architecture diagrams)
- Updated IM integration docs with Slack, slash commands, QA queue, rate limiting, and streaming output sections

### 🔒 Security Enhancements
- Enhanced SSRF protection in RemoteAPIChat: replaced default DialContext with SSRFSafeDialContext; added SSRF URL validation for BaseURL and endpoint in NewRemoteAPIChat and chat methods

## [0.3.4] - 2026-03-19

### 🚀 New Features
- **NEW**: IM Bot Integration — support WeCom, Feishu, and Slack IM channel integration with WebSocket/Webhook modes, streaming support, file upload, and knowledge base integration
- **NEW**: Multimodal Image Support — implement image upload and multimodal image processing with enhanced session management
- **NEW**: Manual Knowledge Download — support downloading manual knowledge content as files with proper filename sanitization and Content-Disposition handling
- **NEW**: NVIDIA Model API — support NVIDIA chat model API with custom endpoint configuration and VLM model support
- **NEW**: Weaviate Vector DB — add Weaviate as a new vector database backend for knowledge retrieval
- **NEW**: AWS S3 Storage — integrate AWS S3 storage adapter with database migrations and configuration UI
- **NEW**: AES-256-GCM Encryption — add AES-256-GCM encryption for API keys at rest for enhanced security
- **NEW**: Built-in MCP Service — add built-in MCP service support for extending agent capabilities
- **NEW**: Multi-Content Messages — enhance message structure to support multi-content messages
- **NEW**: Web Search in AgentQA — add web search option to AgentQA functionality
- **NEW**: Clear Session Messages — add functionality to clear session messages
- **NEW**: Agent Management — add agent management functionality in the frontend
- **NEW**: Knowledge Move — implement knowledge move functionality between knowledge bases
- **NEW**: Chat History & Retrieval Settings — implement chat history and retrieval settings configuration
- **NEW**: Final Answer Tool — introduce final_answer tool and enhance agent duration tracking
- **NEW**: Batch Chunk Deletion — implement batch deletion for chunks to avoid MySQL placeholder limit

### ⚡ Improvements
- Optimized hybrid search by grouping targets and reusing query embeddings for better performance
- Enhanced knowledge search by resolving embedding model keys
- Enhanced AgentStreamDisplay with auto-scrolling, improved styling, and loading indicators
- Enhanced chat model selection logic in session management
- Enhanced input field component with improved handling and sanitization
- Unified dropdown menu styles across components
- Enhanced storage engine configuration and user notifications
- Improved document preview with responsive design and localized fullscreen toggle
- Enhanced agent event emission for final answers and fallback handling
- Enhanced FAQ metadata normalization and sanitization
- Updated LLM configuration to model ID in API and frontend
- Added computed model status for LLM availability in GraphSettings
- Added pulsing animation to stop button and improved loading indicators
- Added language support to summary generation payload
- Enabled parent-child chunking and question generation in KnowledgeBaseEditorModal
- Standardized loading and avatar sizes across components
- Updated storage size calculations for vector embeddings

### 🐛 Bug Fixes
- Fixed Milvus retriever related issues
- Fixed docparser handling of nested linked images and URL parentheses
- Fixed chunk timestamp update to use NOW() for consistency
- Fixed NVIDIA VLM model API default BaseURL
- Fixed auth error messages and unified username validation length
- Enforced 7500 char limit in chunker to prevent embedding API errors
- Fixed builtin engine handling of simple formats
- Fixed dev-app command error on Linux
- Fixed vue-i18n placeholder escaping, computed ref accessor, and missing ru-RU keys
- Fixed multilingual support for TDesign components and locale key synchronization
- Fixed session title word count requirement
- Updated default language setting to Chinese
- Fixed MinIO endpoint format error message
- Fixed storage engine warning display and styling
- Fixed manual download button layout and polish
- Fixed sanitize tab chars and double .md extension in manual download filename

### 📚 Documentation
- Added documentation for Slack IM channel integration
- Added design specification and implementation plan for manual knowledge download

### 🔧 Refactoring
- Streamlined agent document info retrieval and enhanced chunk search logic
- Improved IM tool invocation and result formatting
- Consolidated QA request handling and improved session service interface
- Simplified fullscreen handling and improved styling in document preview
- Updated conversation handling and image description requirements
- Changed tokenization method for improved processing

## [0.3.3] - 2026-03-05

### 🚀 New Features
- **NEW**: Parent-Child Chunking — implement parent-child chunking strategy for enhanced context management with hierarchical chunk retrieval
- **NEW**: Knowledge Base Pinning — support pinning frequently-used knowledge bases for quick access
- **NEW**: Fallback Response — add fallback response handling and UI indicators when no relevant results are found
- **NEW**: Image Icon Detection — add image icon detection and filtering functionality for document processing
- **NEW**: Passage Cleaning for Rerank — add passage cleaning functionality for rerank model to improve relevance scoring
- **NEW**: ListChunksByParentIDs — add ListChunksByParentIDs method and enhance chunk merging logic for parent-child retrieval
- **NEW**: GetUserByTenantID — add GetUserByTenantID functionality to user repository and service

### ⚡ Improvements
- Enhanced Docker setup with entrypoint script and skill management
- Enhanced storage engine connectivity check with auto-creation of buckets
- Enhanced MinerU response handling for document parsing
- Enhanced sidebar functionality and UI responsiveness
- Updated chunk size configurations for knowledge base processing
- Enforced maximum length for tool names in MCPTool for safety
- Updated theme and UI styles across components for visual consistency
- Updated at-icon SVG and enhanced input field component
- Standardized border styles and adjusted component styles for improved consistency

### 🐛 Bug Fixes
- Fixed cleanupCtx created at startup potentially expiring before shutdown

## [0.3.2] - 2026-03-04

### 🚀 New Features
- **NEW**: Knowledge Search — new "Knowledge Search" entry point with semantic retrieval, supporting bringing search results directly into the conversation window
- **NEW**: Parser Engine Configuration — support configuring document parser engines and storage engines for different sources in settings, with per-file-type parser engine selection in knowledge base
- **NEW**: Storage Provider Configuration — support configuring storage providers (local, MinIO, COS, Volcengine TOS) per data source with standardized configuration and backward compatibility
- **NEW**: Milvus Vector Database — added Milvus as a new vector database backend for knowledge retrieval
- **NEW**: Volcengine TOS — added Volcengine TOS object storage support
- **NEW**: Mermaid Rendering — support mermaid diagram rendering in chat with fullscreen viewer, zoom, pan, toolbar and export
- **NEW**: Batch Conversation Management — batch management and delete all sessions functionality
- **NEW**: Remote URL Knowledge Creation — support creating knowledge entries from remote file URLs
- **NEW**: Async Knowledge Re-parse — async API for re-processing existing knowledge documents
- **NEW**: User Memory Graph Preview — preview of user-level memory graph visualization
- **NEW**: Tenant Access Authorization — tenant access authorization in TenantHandler
- **NEW**: Database Query Tool — built-in database query tool for agents with automatic tenant isolation and soft-delete filtering

### ⚡ Improvements
- Image rendering in local storage mode during conversations with optimized streaming image placeholders
- Embedded document preview component for previewing user-uploaded original files
- Knowledge base, agent, and shared space list page interaction redesign with improved UI elements
- Storage configuration standardization with enhanced backward compatibility
- Dynamic file service resolution for knowledge extraction
- SSRF safety checks enhanced in MinerUCloudReader
- Nginx configuration improved for file handling
- Dockerfile and build scripts with customizable APT mirror support
- System information display with database version
- Path and filename validation security utilities
- Vector embeddings indexing enhanced with TagID and IsRecommended fields
- Korean (한국어) README translation

### 🐛 Bug Fixes
- Handle thinking content in Ollama chat responses
- Batch manage dialog now loads all sessions independently from API
- Prevent modal from closing when text selection extends beyond dialog boundary
- Handle empty metadata case in Knowledge struct
- Swagger interface documentation generation error resolved
- Auth form validation check to handle non-boolean responses
- Helm frontend APP_HOST env default value corrected

### 🗑️ Removals
- Removed Lite edition support and related configurations

## [0.3.1] - 2026-02-10

### 🚀 New Features
- **NEW**: Remote Backend Support — support remote backend and HTTPS proxy configuration
- **NEW**: Enhanced Document Upload — expanded document upload capabilities in KnowledgeBase component

### ⚡ Improvements
- Enhanced resource management in ListSpaceSidebar and KnowledgeBaseList

### 🐛 Bug Fixes
- Add clipboard API fallback for non-secure contexts
- DuckDB spatial extension not found error
- Data analysis knowledge files loaded via presigned URLs

## [0.3.0] - 2026-02-09

### 🚀 New Features
- **NEW**: Shared Space — shared space management with member invitations, shared knowledge bases and agents across members, tenant isolation for retrieval
- **NEW**: Agent Skills — agent skills with preloaded skills for smart-reasoning agent, sandbox-based execution environment
- **NEW**: Bing Search — added Bing as a new web search provider
- **NEW**: Agent Thinking Mode — support thinking mode for agents, strip thinking content from output
- **NEW**: Web Fetch DNS pinning and validation improvements
- **NEW**: FAQ matched question field in search results
- **NEW**: Knowledge base mentioned-only retrieval option

### ⚡ Improvements
- Redis ACL support with `REDIS_USERNAME` environment variable
- Configurable global log level via environment variable
- Use `num_ctx` instead of `truncate` for embedding truncation (Ollama compatibility)
- Large FAQ imports offloaded to object storage
- Unified card styles and layout consistency across components
- OCR module restructured with centralized configuration
- Enhanced MCP tool name and description handling for security
- Structured logger replacing standard log in main and recovery middleware

### 🐛 Bug Fixes
- MCP Client connection state not marked as closed after SSE connection loss
- Clear tag selection state when re-entering knowledge base
- Rune handling for correct chunk merging
- Host extraction from completion_url handling both v1 and non-v1 endpoints
- SQL injection prevention via OR conditions with comprehensive validation
- Switch to append mode on retry to prevent data loss
- Parser file_extension for markitdown compatibility

### 🔒 Security Enhancements
- SSRF-safe HTTP client for URL imports and fetching
- SQL validation logic centralized and simplified
- Sandbox-based agent skills execution with security isolation

## [0.2.10] - 2026-01-16

### 🚀 New Features
- **NEW**: Support for deleting document type tags
- **NEW**: Google provider for web search
- **NEW**: Added multiple mainstream model providers including GPUStack
- **NEW**: AgentQA request field support
- **NEW**: FAQ batch import dry run functionality
- **NEW**: Support tenant ID and keyword simultaneous search
- **NEW**: FAQ import result persistence display
- **NEW**: SeqID auto-increment tag support
- **NEW**: Support adding similar questions to FAQ entries
- **NEW**: FAQ import success entry details display
- **NEW**: Enhanced task ID generator replacing UUID

### ⚡ Improvements
- **IMPROVED**: Chunk merge/split logic with validation
- **IMPROVED**: FAQ index update and deletion performance optimization
- **IMPROVED**: Batch indexing with concurrent save optimization
- **IMPROVED**: Retriever engine checks and mapping exposure refactored
- **IMPROVED**: FAQ import and validation logic merged
- **IMPROVED**: Error handling and unused code removal

### 🐛 Bug Fixes
- **FIXED**: Disabled stdio transport to prevent command injection risks
- **FIXED**: FAQ update duplicate check logic
- **FIXED**: Migration script table name spelling error
- **FIXED**: Unused tag cleanup ignoring soft-deleted records
- **FIXED**: FAQ import tag cleanup logic
- **FIXED**: FAQ entry tag change not updating issue
- **FIXED**: Ensure "Uncategorized" tag appears first
- **FIXED**: Potential crash from slice out of bounds
- **FIXED**: Tag deletion using correct ID field
- **FIXED**: FAQ tag filtering using seq_id instead of id type issue
- **FIXED**: Critical vulnerability V-001 resolved
- **FIXED**: Added EncodingFormat parameter for ModelScope embedding models
- **FIXED**: Secure command execution with sandbox for doc_parser


## [0.2.9] - 2026-01-10

### 🚀 New Features
- **NEW**: Batch tag name supplement in search results
- **NEW**: Return updated data when updating FAQ entries
- **NEW**: Convert uncategorized FAQ entries to "Uncategorized" tag

## [0.2.8] - 2025-12-31

### 🚀 New Features
- **NEW**: Data Analyst Agent & Tools
  - Added built-in Data Analyst agent
  - Added DataSchema tool for retrieving schema from CSV/Excel files
  - Support for agent file type restrictions
- **NEW**: Thinking Mode Support
  - Added configuration support for Thinking mode
  - Added Thinking field to Summary configuration
- **NEW**: Enhanced File & Storage Management
  - Support listing MinIO buckets and permissions
  - Configurable file upload size limits
  - Full-text merge view mode
- **NEW**: Conversation Enhancements
  - Added option to disable automatic title generation
  - Enhanced KnowledgeQAStream parameters
  - Support for streaming response types and tool calls
- **NEW**: System & Configuration
  - Added `WEKNORA_VERSION` environment variable support
  - APK mirror configuration support in Docker
  - Enhanced chunking separator options
  - FAQ two-level priority tag filtering
  - Update index fields when batch updating tags

### ⚡ Improvements
- **IMPROVED**: Agent & Model Handling
  - Unified agent not ready message logic
  - Optimized built-in agent configuration synchronization
  - Removed model locking logic to allow free switching
  - Enhanced model selection and error handling
- **IMPROVED**: Refactoring
  - Simplified session creation request structure
  - Converted knowledgeRefs to References type
  - Refactored SSE stream setup
  - Refactored bucket policy parsing logic
  - Streamlined Docker package installation

### 🐛 Bug Fixes
- **FIXED**: Localization placeholder display issues
- **FIXED**: Duplicate tag creation and stream response parsing
- **FIXED**: Missing WebSearchStateService in parallel search
- **FIXED**: Model list refresh on settings popup close
- **FIXED**: Asynq Redis DB configuration
- **FIXED**: Menu deletion logic and count updates
- **FIXED**: OpenAI API compatibility (exclude ChatTemplateKwargs)
- **FIXED**: Handled Nginx 413 (Payload Too Large) requests
- **FIXED**: Added existence check for embeddings table in tag_id migration


## [0.2.6] - 2025-12-29

### 🚀 New Features
- **NEW**: Custom Agent System
  - Support for creating, configuring, and selecting custom agents
  - Agent feature indicators display with MCP service capability support
  - Built-in agent sorting logic ensuring multi-turn conversation auto-enabled in agent mode
  - Agent knowledge base selection modes: all/specified/disabled

- **NEW**: Helm Chart for Kubernetes Deployment
  - Complete Helm chart for Kubernetes deployment
  - Neo4j template support for GraphRAG functionality
  - Versioned image tags and official images compatibility

- **NEW**: Enhanced FAQ Management
  - FAQ entry retrieval API supporting single entry query by ID
  - FAQ list sorting by update time (ascending/descending)
  - Enhanced FAQ search with field-specific search (standard question/similar questions/answer/all)
  - Batch update exclusion for FAQ entries in ByTag operations
  - Tag deletion with content_only mode to delete only tag contents

- **NEW**: Multi-Platform Model Adaptation
  - Support for multiple platform model configurations
  - Title generation model configuration
  - Knowledge base selection mode without mandatory rerank model check

- **NEW**: Korean Language Support
  - Added Korean (한국어) internationalization support

### ⚡ Improvements
- **IMPROVED**: Knowledge Base Operations
  - Async knowledge base deletion with background cleanup via ProcessKBDelete
  - Multi-knowledge base search support with specified file ID filtering
  - Optimized knowledge chunk pagination with type-specific search and sorting logic
  - Enhanced SearchKnowledgeRequest structure with backward compatibility

- **IMPROVED**: Prompt Template System
  - Restructured prompt template system with multi-scenario template configuration
  - Unified system prompts with optimized agent selector interface

- **IMPROVED**: Tag Management
  - Enhanced tag deletion with ID exclusion support
  - Async index deletion task for optimized deletion flow
  - Batch TagID update functionality
  - Optimized tag name batch queries for improved efficiency

- **IMPROVED**: API Documentation
  - Updated API documentation links to new paths
  - Added knowledge search API documentation
  - Enhanced FAQ and tag deletion interface documentation
  - Removed hardcoded host configuration from Swagger docs

### 🐛 Bug Fixes
- **FIXED**: Tag ID handling logic for empty strings and UntaggedTagID conditions
- **FIXED**: JSON query compatibility for different database types (MySQL/PostgreSQL)
- **FIXED**: GORM batch insert issue where zero-value fields (IsEnabled, Flags) were ignored
- **FIXED**: Helm chart versioned image tags and runAsNonRoot compatibility

### 🔧 Refactoring
- **REFACTORED**: Removed security validation and length limits, simplified input processing logic
- **REFACTORED**: Enhanced agent configuration with improved selection and state management

## [0.2.5] - 2025-12-22

### 🚀 New Features
- **NEW**: In-Input Knowledge Base and File Selection
  - Support selecting knowledge bases and files directly within the input box
  - Display @mentioned knowledge bases and files in message stream
  - Dynamic placeholder text based on knowledge base and web search status

- **NEW**: API Key Authentication Support
  - Added API Key authentication mechanism
  - Optimized Swagger documentation security configuration
  - Disabled Swagger documentation access in non-production environments by default

- **NEW**: User Registration Control
  - Added `DISABLE_REGISTRATION` environment variable to control user registration

- **NEW**: User Conversation Model Selection
  - Added user conversation model selection state management with store two-way binding

### 🔒 Security Enhancements
- **ENHANCED**: MCP stdio transport security validation to prevent command injection attacks
- **ENHANCED**: SQL security validation rebuilt using PostgreSQL official parser for enhanced query protection
- **ENHANCED**: Security policy updated with vulnerability reporting guidelines

### ⚡ Improvements
- **IMPROVED**: Streaming rendering mechanism optimized for token-by-token Markdown content parsing
- **IMPROVED**: FAQ import progress refactored to use Redis for task state storage
- **IMPROVED**: Enhanced knowledge base and search functionality logic

### 🐛 Bug Fixes
- **FIXED**: Corrected knowledge ID retrieval in FAQ import tasks
- **FIXED**: Force removal of legacy vlm_model_id field from knowledge_bases table
- **FIXED**: Disabled Ollama option for ReRank models in model management with tooltip


## [0.2.4] - 2025-12-17

### 🚀 New Features
- **NEW**: FAQ Entry Export
  - Support CSV format export for FAQ entries

- **NEW**: Asynchronous Knowledge Base Copy
  - Progress tracking and incremental sync support
  - Improved SourceID conversion logic and tag mapping for knowledge base copying

- **NEW**: FAQ Index Type Separation
  - Added is_enabled field filtering and batch update optimization

- **NEW**: Swagger API Documentation
  - Enhanced Swagger API documentation generation

### 🐛 Bug Fixes
- **FIXED**: Optimized tag mapping logic and FAQ cloning during knowledge base copy
- **FIXED**: Adjusted Knowledge struct Metadata field type to json.RawMessage
- **FIXED**: Added tenant information to context during knowledge base copy
- **FIXED**: Database migration compatibility with older versions

## [0.2.3] - 2025-12-16

### 🚀 New Features
- **NEW**: Chat Message Image Preview
  - Support image preview in chat messages
  - Updated Agent prompts to include image-text result output
  - Image information display in knowledge search and list tools

- **NEW**: FAQ Answer Strategy Field
  - Support 'all' (return all answers) and 'random' (randomly return one answer) modes

- **NEW**: FAQ Recommendation Field
  - Added recommendation field for FAQ entries
  - Support batch update by tag

### ⚡ Improvements
- **IMPROVED**: Optimized async task retry logic to update failure status only on last retry
- **IMPROVED**: Enhanced hybrid search result fusion strategy
- **IMPROVED**: Updated MinIO, Jaeger, and Neo4j image versions for stability

### 🐛 Bug Fixes
- **FIXED**: Environment variable saving logic in MCP service dialog
- **FIXED**: AUTO_RECOVER_DIRTY environment variable logic in database migration, enabled by default

### ⚡ Infrastructure Improvements
- **IMPROVED**: Updated Dockerfile with uvx permission adjustments and Node version upgrade

## [0.2.2] - 2025-12-15

### 🚀 New Features
- **NEW**: FAQ Answer Strategy Configuration
  - Added answer strategy field for FAQ entries, supporting `all` (return all answers) and `random` (randomly return one answer) modes
  - More flexible FAQ response control

- **NEW**: FAQ Recommendation Feature
  - Added recommendation field for FAQ entries to mark recommended Q&A
  - Support batch update of FAQ recommendation status by tag
  - Optimized tag deletion logic

- **NEW**: Document Summary Status Tracking
  - Added `SummaryStatus` field to Knowledge struct
  - Support tracking document summary generation status

### ⚡ Infrastructure Improvements
- **IMPROVED**: Docker Build Optimization
  - Fixed system package conflicts during pip dependency installation with `--break-system-packages` parameter
  - Adjusted uvx permission configuration
  - Upgraded Node version

- **IMPROVED**: Database Initialization
  - Optimized database initialization logic with conditional embeddings handling

### 🐛 Bug Fixes
- **FIXED**: Corrected `MINIO_USE_SSL` environment variable parsing logic

## [0.2.1] - 2025-12-08

### 🚀 New Features
- **NEW**: Qdrant Vector Database Support
  - Full integration with Qdrant as retriever engine
  - Support for both vector similarity search and full-text keyword search
  - Dynamic collection creation based on embedding dimensions (e.g., `weknora_embeddings_768`)
  - Multilingual tokenizer support for Chinese/Japanese/Korean text search
  - Professional Chinese word segmentation using jieba for keyword queries

### ⚡ Infrastructure Improvements
- **IMPROVED**: Docker Compose Profile Management
  - Added profiles for optional services: `minio`, `qdrant`, `neo4j`, `jaeger`, `full`
  - Enhanced `dev.sh` script with `--minio`, `--qdrant`, `--neo4j`, `--jaeger`, `--full` flags
  - Pinned Qdrant Docker image version to `v1.16.2` for stability
- **IMPROVED**: Database Migration System
  - Added automatic dirty state recovery for failed migrations
  - Added Neo4j connection retry mechanism with exponential backoff
  - Improved migration error handling and logging
- **IMPROVED**: Retriever Engine Configuration
  - Retriever engines now auto-configured from `RETRIEVE_DRIVER` environment variable
  - No longer required to write retriever config during user registration
  - Added `GetEffectiveEngines()` method for dynamic engine resolution
  - Centralized engine mapping in `types/tenant.go`

### 🐛 Bug Fixes
- **FIXED**: Qdrant keyword search returning empty results for Chinese queries
- **FIXED**: Image URL validation logic simplified for better compatibility

### 📚 Documentation
- Added Qdrant configuration examples in docker-compose files

## [0.2.0] - 2025-12-05

### 🚀 Major Features
- **NEW**: ReACT Agent Mode
  - Added ReACT Agent mode that can use built-in tools to retrieve knowledge bases
  - Support for calling user-configured MCP tools and web search tools to access external services
  - Multiple iterations and reflection to provide comprehensive summary reports
  - Cross-knowledge base retrieval support, allowing selection of multiple knowledge bases
- **NEW**: Model Management System
  - Centralized model configuration
  - Added model selection in knowledge base settings page
  - Built-in model sharing functionality across multiple tenants
  - Tenants can use shared models but are restricted from editing or viewing model details
- **NEW**: Multi-Type Knowledge Base Support
  - Support for creating FAQ and document knowledge base types
  - Folder import functionality
  - URL import functionality
  - Tag management system
  - Online knowledge entry capability
- **NEW**: FAQ Knowledge Base
  - New FAQ-type knowledge base
  - Batch import and batch delete functionality
  - Online FAQ entry
  - Online FAQ testing capability
- **NEW**: Conversation Strategy Configuration
  - Support for configuring Agent models and normal mode models
  - Configurable retrieval thresholds
  - Online Prompt configuration
  - Precise control over multi-turn conversation behavior and retrieval execution methods
- **NEW**: Web Search Integration
  - Support for extensible web search engines
  - Built-in DuckDuckGo search engine
- **NEW**: MCP Tool Integration
  - Support for extending Agent capabilities through MCP
  - Built-in uvx and npx MCP launcher tools
  - Support for three transport methods: Stdio, HTTP Streamable, and SSE

### 🎨 UI/UX Improvements
- **REDESIGNED**: Conversation interface with Agent mode/normal mode switching
  - Added Agent mode/normal mode toggle in conversation input box
  - Support for enabling/disabling web search
  - Support for selecting conversation models
- **REDESIGNED**: Login page UI adjustments
- **ENHANCED**: Session list with time-ordered grouping
- **NEW**: Quick Actions area for unified UI visual effects
- **IMPROVED**: Knowledge base list cards
  - Display knowledge base type, knowledge count, build status
  - Show advanced settings capabilities
- **NEW**: Breadcrumb navigation in FAQ and document list pages
  - Quick navigation and knowledge base switching
- **ENHANCED**: Knowledge base settings in document list page
- **REDESIGNED**: Knowledge base settings page
  - Separate configuration for knowledge base type, models, chunking methods, and advanced settings
- **NEW**: Global settings page for permissions
  - Configure models, web search, MCP services, and Agent mode
- **IMPROVED**: Chunk details page display
- **NEW**: Knowledge classification and tagging support
- **ENHANCED**: Conversation flow page with tool call execution process display

### ⚡ Infrastructure Upgrades
- **NEW**: MQ-based async task management
  - Introduced MQ for async task state maintenance
  - Ensures task integrity even after service abnormal restart
- **NEW**: Automatic database migration
  - Support for automatic database schema and data migration during version upgrades
- **NEW**: Fast development mode
  - Added docker-compose.dev.yml file for quick development environment startup
  - Improved development workflow efficiency
- **IMPROVED**: Log structure optimization
- **NEW**: Event subscription and publishing mechanism
  - Support for event handling at various steps in user query processing flow

### 🐛 Bug Fixes
- Various bug fixes and stability improvements

### 📚 Documentation Updates
- Updated README files with v0.2.0 highlights (English, Chinese, Japanese)
- Added latest updates section in all README files
- Updated architecture diagrams and feature matrices
## [0.1.6] - 2025-11-24

### Document Parser Enhancements
- NEW: Added CSV, XLSX, XLS file parsing support (spreadsheet processing, tabular data extraction)
- NEW: Web page parser (dedicated class, optimized web image encoding, improved dependency management)

### Document Processing Improvements
- NEW: MarkdownTableUtil (reduced whitespace, improved table readability/consistency)
- NEW: Document model class (structured models for type safety, optimized config/parsing logic)
- UPGRADED: Docx2Parser (enhanced timeout handling, better image processing, optimized OCR backend)

### Internationalization
- NEW: English/Russian multi-language support (vue-i18n integration, translated UI/text/errors, multilingual docs for knowledge graph/MCP config)

### Bug Fixes
- Fixed menu component integration issues
- Fixed Darwin (macOS) memory check regex error (resolved empty output)
- Fixed model availability check (unified logic, auto ":latest" tag, prevented duplicate pull calls)
- Fixed Docker Compose security vulnerability (addressed writable filesystem issue)

### Refactoring & Optimization
- Refactored parser logging/API checks (simplified exception handling, better error reporting)
- Refactored chunk processing (removed redundant header handling, updated examples)
- Refactored module organization (docreader structure, proto/client imports, Docker config, absolute imports)

### Documentation Updates
- Updated API Key acquisition docs (web registration + account page retrieval)
- Updated Docker Compose setup guide (comprehensive instructions, config adjustments)
- Updated multilingual docs (added knowledge graph/MCP config guides, directory structure)
- Removed deprecated hybrid search API docs

### Code Cleanup
- Removed redundant Docker build parameters
- Updated .gitignore rules
- Optimized import statements/type hints
- Cleaned redundant logging/comments

### CI/CD Improvements
- Added new CI/CD trigger branches
- Added build concurrency control
- Added disk space cleanup automation

## [0.1.5] - 2025-10-20

### Features & Enhancements
- Added multi-knowledgebases operation support and management (UI & backend logic)
- Enhanced tenant information management: New tenant page with user-friendly storage quota and usage rate display (see TenantInfo.vue)
- Initialization Wizard improvements: Stricter form validation, VLM/OpenAI compatible URL verification, and multimodal file upload preview & validation (see InitializationContent.vue)
- Backend: API Key automatic generation and update logic (see types.Tenant & tenantService.UpdateTenant)

### UI / UX
- Restructured settings page and initialization page layouts; optimized button states, loading states, and prompt messages; improved upload/preview experience
- Enhanced menu component: Multi-knowledgebase switching and pre-upload validation logic (see menu.vue)
- Hidden/protected sensitive information (e.g., API Keys) and added copy interaction prompts (see TenantInfo.vue)

### Security Fixes
- Fixed potential frontend XSS vulnerabilities; enhanced input validation and Content Security Policy
- Hidden API Keys in UI and improved copy behavior prompts to strengthen information leakage protection

### Bug Fixes
- Resolved OCR/AVX support-related issues and image parsing concurrency errors
- Fixed frontend routing/login redirection issues and file download content errors
- Fixed docreader service health check and model prefetching issues

### DevOps / Building
- Improved image building scripts: Enhanced platform/architecture detection (amd64 / arm64) and injected version information during build (see get_version.sh & build_images.sh)
- Refined Makefile and build process to facilitate CI injection of LDFLAGS (see Makefile)
- Improved usage and documentation for scripts and migration tools (migrate) (see migrate.sh)

### Documentation
- Updated README and multilingual documentation (EN/CN/JA) along with release/CHANGELOG (see CHANGELOG.md & README.md for details)
- Added MCP server usage instructions and installation guide (see mcp-server/INSTALL.md)

### Developer / Internal API Changes (For Reference)
- New/updated backend system information response structure: handler.GetSystemInfoResponse
- Tenant data structure and JSON storage fields: types.Tenant

## [0.1.4] - 2025-09-17

### 🚀 Major Features
- **NEW**: Multi-knowledgebases operation support
  - Added comprehensive multi-knowledgebase management functionality
  - Implemented multi-data source search engine configuration and optimization logic
  - Enhanced knowledge base switching and management in UI
- **NEW**: Enhanced tenant information management
  - Added dedicated tenant information page
  - Improved user and tenant management capabilities

### 🎨 UI/UX Improvements
- **REDESIGNED**: Settings page with improved layout and functionality
- **ENHANCED**: Menu component with multi-knowledgebase support
- **IMPROVED**: Initialization configuration page structure
- **OPTIMIZED**: Login page and authentication flow

### 🔒 Security Fixes
- **FIXED**: XSS attack vulnerabilities in thinking component
- **FIXED**: Content Security Policy (CSP) errors
- **ENHANCED**: Frontend security measures and input sanitization

### 🐛 Bug Fixes
- **FIXED**: Login direct page navigation issues
- **FIXED**: App LLM model check logic
- **FIXED**: Version script functionality
- **FIXED**: File download content errors
- **IMPROVED**: Document content component display

### 🧹 Code Cleanup
- **REMOVED**: Test data functionality and related APIs
- **SIMPLIFIED**: Initialization configuration components
- **CLEANED**: Redundant UI components and unused code


## [0.1.3] - 2025-09-16

### 🔒 Security Features
- **NEW**: Added login authentication functionality to enhance system security
- Implemented user authentication and authorization mechanisms
- Added session management and access control
- Fixed XSS attack vulnerabilities in frontend components

### 📚 Documentation Updates
- Added security notices in all README files (English, Chinese, Japanese)
- Updated deployment recommendations emphasizing internal/private network deployment
- Enhanced security guidelines to prevent information leakage risks
- Fixed documentation spelling issues

### 🛡️ Security Improvements
- Hide API keys in UI for security purposes
- Enhanced input sanitization and XSS protection
- Added comprehensive security utilities

### 🐛 Bug Fixes
- Fixed OCR AVX support issues
- Improved frontend health check dependencies
- Enhanced Docker binary downloads for target architecture
- Fixed COS file service initialization parameters and URL processing logic

### 🚀 Features & Enhancements
- Improved application and docreader log output
- Enhanced frontend routing and authentication flow
- Added comprehensive user management system
- Improved initialization configuration handling

### 🛡️ Security Recommendations
- Deploy WeKnora services in internal/private network environments
- Avoid direct exposure to public internet
- Configure proper firewall rules and access controls
- Regular updates for security patches and improvements

## [0.1.2] - 2025-09-10

- Fixed health check implementation for docreader service
- Improved query handling for empty queries
- Enhanced knowledge base column value update methods
- Optimized logging throughout the application
- Added process parsing documentation for markdown files
- Fixed OCR model pre-fetching in Docker containers
- Resolved image parser concurrency errors
- Added support for modifying listening port configuration

## [0.1.0] - 2025-09-08

- Initial public release of WeKnora.
- Web UI for knowledge upload, chat, configuration, and settings.
- RAG pipeline with chunking, embedding, retrieval, reranking, and generation.
- Initialization wizard for configuring models (LLM, embedding, rerank, retriever).
- Support for local Ollama and remote API models.
- Vector backends: PostgreSQL (pgvector), Elasticsearch; GraphRAG support.
- End-to-end evaluation utilities and metrics.
- Docker Compose for quick startup and service orchestration.
- MCP server support for integrating with MCP-compatible clients.

[0.5.0]: https://github.com/Tencent/WeKnora/tree/v0.5.0
[0.4.0]: https://github.com/Tencent/WeKnora/tree/v0.4.0
[0.3.6]: https://github.com/Tencent/WeKnora/tree/v0.3.6
[0.3.5]: https://github.com/Tencent/WeKnora/tree/v0.3.5
[0.3.4]: https://github.com/Tencent/WeKnora/tree/v0.3.4
[0.3.3]: https://github.com/Tencent/WeKnora/tree/v0.3.3
[0.3.2]: https://github.com/Tencent/WeKnora/tree/v0.3.2
[0.3.1]: https://github.com/Tencent/WeKnora/tree/v0.3.1
[0.3.0]: https://github.com/Tencent/WeKnora/tree/v0.3.0
[0.2.10]: https://github.com/Tencent/WeKnora/tree/v0.2.10
[0.2.9]: https://github.com/Tencent/WeKnora/tree/v0.2.9
[0.2.8]: https://github.com/Tencent/WeKnora/tree/v0.2.8
[0.2.7]: https://github.com/Tencent/WeKnora/tree/v0.2.7
[0.2.6]: https://github.com/Tencent/WeKnora/tree/v0.2.6
[0.2.5]: https://github.com/Tencent/WeKnora/tree/v0.2.5
[0.2.4]: https://github.com/Tencent/WeKnora/tree/v0.2.4
[0.2.3]: https://github.com/Tencent/WeKnora/tree/v0.2.3
[0.2.2]: https://github.com/Tencent/WeKnora/tree/v0.2.2
[0.2.1]: https://github.com/Tencent/WeKnora/tree/v0.2.1
[0.2.0]: https://github.com/Tencent/WeKnora/tree/v0.2.0
[0.1.4]: https://github.com/Tencent/WeKnora/tree/v0.1.4
[0.1.3]: https://github.com/Tencent/WeKnora/tree/v0.1.3
[0.1.2]: https://github.com/Tencent/WeKnora/tree/v0.1.2
[0.1.0]: https://github.com/Tencent/WeKnora/tree/v0.1.0
