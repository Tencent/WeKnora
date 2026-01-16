# Changelog

All notable changes to this project will be documented in this file.

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
