<p align="center">
  <picture>
    <img src="./docs/images/logo.png" alt="WeKnora Logo" height="120"/>
  </picture>
</p>

<p align="center">
  <picture>
    <a href="https://trendshift.io/repositories/15289" target="_blank">
      <img src="https://trendshift.io/api/badge/repositories/15289" alt="Tencent%2FWeKnora | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/>
    </a>
  </picture>
</p>
<p align="center">
    <a href="https://weknora.weixin.qq.com" target="_blank">
        <img alt="Official Website" src="https://img.shields.io/badge/Official Website-WeKnora-4e6b99">
    </a>
    <a href="https://chatbot.weixin.qq.com" target="_blank">
        <img alt="WeChat Dialog Open Platform" src="https://img.shields.io/badge/WeChat Dialog Open Platform-5ac725">
    </a>
    <a href="https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd" target="_blank">
        <img alt="Chrome Extension" src="https://img.shields.io/badge/Chrome Extension-WeKnora-4285F4">
    </a>
    <a href="https://clawhub.ai/lyingbug/weknora" target="_blank">
        <img alt="ClawHub Skill" src="https://img.shields.io/badge/ClawHub Skill-WeKnora-ff6b35">
    </a>
    <a href="https://github.com/Tencent/WeKnora/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="Version" src="https://img.shields.io/badge/version-0.5.1-2e6cc4?labelColor=d4eaf7">
    </a>
</p>

<p align="center">
| <b>English</b> | <a href="./README_CN.md"><b>简体中文</b></a> | <a href="./README_JA.md"><b>日本語</b></a> | <a href="./README_KO.md"><b>한국어</b></a> |
</p>

<p align="center">
  <h4 align="center">

  [Overview](#-overview) • [Architecture](#-architecture) • [Key Features](#-key-features) • [Getting Started](#-getting-started) • [API Reference](#-api-reference) • [Developer Guide](#-developer-guide)
  
  </h4>
</p>

# 💡 WeKnora — Turn Documents into Living Knowledge with RAG, Agents and Auto-Wiki

## 📌 Overview

[**WeKnora**](https://weknora.weixin.qq.com) is an open-source, LLM-powered knowledge framework built for enterprise-grade document understanding, semantic retrieval, and autonomous reasoning.

It is organized around three core capabilities: **RAG-based Quick Q&A** for everyday lookups, a **ReAct Agent** that autonomously orchestrates retrieval, MCP tools and web search to handle complex multi-step tasks, and a brand-new **Wiki Mode** in which agents distill raw documents into a self-maintaining, interlinked markdown knowledge base with an interactive knowledge graph. Combined with multi-source ingestion (Feishu / Notion / Yuque, and growing), 20+ LLM provider integrations, full Langfuse observability, and a fully self-hostable modular architecture, WeKnora turns scattered documents into a queryable, reasoning-capable, continuously evolving knowledge asset.

The framework supports auto-syncing knowledge from Feishu, Notion, and Yuque (more data sources coming soon), handles 10+ document formats including PDF, Word, images, and Excel, and can serve Q&A directly through IM channels like WeCom, Feishu, Slack, and Telegram. It is compatible with major LLM providers including OpenAI, DeepSeek, Qwen (Alibaba Cloud), Zhipu, Hunyuan, Gemini, MiniMax, NVIDIA, and Ollama. Its fully modular design allows swapping LLMs, vector databases, and storage backends, with support for local and private cloud deployment ensuring complete data sovereignty. WeKnora also integrates with **Langfuse** for comprehensive observability into agent reasoning, token usage, and pipeline tracing.


## ✨ Latest Updates

**v0.5.1 Highlights:**

- **Wiki Mode**: A brand-new agent-driven Wiki knowledge system that automatically distills raw documents into interlinked markdown pages. It ships with a dedicated WikiBrowser and an interactive knowledge graph that visualizes references and relationships between pages, helping teams grow a structured, continuously evolving knowledge base from their own materials.
- **Observability**: Integrated Langfuse for agent ReAct loop, LLM token tracking, tool calls, and asynq pipeline tracing, providing deep visibility into agent reasoning and system performance.
- **Customizable Indexing Strategy**: Independently configure and toggle Vector Search, Keyword Search (Hybrid), Wiki, and Knowledge Graph indexing per knowledge base.
- **Vector Store UI & Per-KB Binding**: Full frontend management for Vector Stores with connectivity testing, plus the ability to bind distinct vector databases to specific knowledge bases.
- **Yuque Connector**: Yuque data source integration with full and incremental fetch, enabling seamless synchronization of Yuque documents.
- **WeChat Mini Program**: New lightweight mobile client (`miniprogram/`) for configuring WeKnora API access, picking knowledge bases, importing URLs, and chatting directly from WeChat.
- **Knowledge Base List View & Batch Operations**: Document management now supports a list view alongside cards, multi-select with a floating action bar, and batch delete for large knowledge bases.
- **Conversation & IM Workflow**: Tenant-wide IM Channels overview under the user menu, conversation list keyword search, user-scoped session pinning, and clear IM-source labels for chats coming from IM channels.
- **Bug Fixes**: Fixed LaTeX flashing during streaming responses (#1056), removed the default 100-page DOCX parsing cap, dropped IM pipeline-level timeouts that killed multi-round agent reasoning, isolated IM sessions per agent, hardened Wiki ingest against malformed JSON / failed-operation loss, and made encrypted-field decryption fail loudly instead of silently returning empty data.

<details>
<summary><b>Earlier Releases</b></summary>

**v0.4.0 Highlights:**

- **[Knowledge Assistant](https://weknora.weixin.qq.com/platform)**: Cloud-hosted knowledge assistant service for quick onboarding without local deployment
- **WeKnora Cloud**: WeKnora Cloud provider with hosted LLM models and document parsing service, credential management and status checks
- **[Chrome Extension](https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd)**: Browser extension for web page knowledge capture
- **[ClawHub Skill](https://clawhub.ai/lyingbug/weknora)**: ClawHub Skill marketplace integration for one-click agent skill installation
- **WeChat IM Integration**: WeChat channel adapter with QR code login and long-polling message support
- **Attachment Processing**: File attachment support in chat pipeline with content formatting and metadata injection
- **Azure OpenAI Provider**: Full Azure OpenAI support for chat, VLM, and embedding models with deployment name preservation and dimensions parameter
- **Alibaba Cloud OSS Storage**: Object storage support via S3-compatible mode with configuration UI, connectivity test, and multi-language i18n
- **Notion Connector**: Notion data source integration with API client, markdown renderer, and Connector interface
- **Baidu & Ollama Web Search**: Added Baidu and Ollama as web search providers
- **VectorStore Management**: Full VectorStore CRUD with entity, repository, service layer, connection testing, and API endpoints
- **Bug Fixes**: Fixed Azure OpenAI endpoint handling, embedding truncation, IM citation tag stripping, neo4j Go 1.24 Windows compatibility, and OSS signature issues


**v0.3.6 Highlights:**

- **ASR (Automatic Speech Recognition)**: Integrated ASR model support with audio file upload, in-document audio preview, and transcription capabilities
- **Data Source Auto-Sync (Feishu)**: Complete data source management with Feishu Wiki/Drive auto-sync, incremental and full sync, sync logs, and tenant isolation
- **OIDC Authentication**: OpenID Connect login support with auto-discovery, custom endpoints, and user info mapping
- **IM Quote/Reply Context**: Quoted messages extracted in IM channels and injected into LLM prompts for contextual replies; anti-hallucination for non-text quotes
- **Thread-Based IM Sessions**: Per-thread session mode for IM channels (Slack, Mattermost, Feishu, Telegram), enabling multi-user collaboration within threads
- **Document Summarization**: AI-generated document summaries with configurable input limits and a dedicated summary section in document detail view
- **Tavily Web Search**: Added Tavily as a web search provider; refactored web search provider architecture for extensibility
- **MCP Auto-Reconnection**: Automatic reconnection for MCP tool calls when server connection is lost
- **Parallel Tool Calling**: Concurrent execution of multiple agent tool calls via errgroup for faster complex task handling
- **Agent @Mention Scope Restriction**: User @mentions restricted to agent's allowed knowledge base scope, preventing unauthorized access
- **Login Page Performance**: Removed all backdrop-filter blur effects, reduced animations, added GPU compositing hints for faster page load

**v0.3.5 Highlights:**

- **Telegram, DingTalk & Mattermost IM Integration**: Added Telegram bot (webhook/long-polling, streaming via editMessageText), DingTalk bot (webhook/Stream mode, AI Card streaming), and Mattermost adapter; IM channel coverage now includes WeCom, Feishu, Slack, Telegram, DingTalk, and Mattermost
- **IM Slash Commands & QA Queue**: Pluggable slash-command system (/help, /info, /search, /stop, /clear) with a bounded QA worker pool, per-user rate limiting, and Redis-based multi-instance coordination
- **Suggested Questions**: Agents surface context-aware suggested questions based on configured knowledge bases; image knowledge automatically enqueues question generation
- **VLM Auto-Describe MCP Tool Images**: When MCP tools return images, the agent generates text descriptions via the configured VLM model, enabling image content to be used by text-only LLMs
- **Novita AI Provider**: New LLM provider with OpenAI-compatible API supporting chat, embedding, and VLLM model types
- **MCP Tool Name Stability**: Tool names now based on service name (stable across reconnections) instead of UUID; unique name constraint added; frontend formats names into human-readable form
- **Channel Tracking**: Knowledge entries and messages record source channel (web/api/im/browser_extension) for traceability
- **Bug Fixes**: Fixed agent empty response when no knowledge base is configured, UTF-8 truncation in summaries for Chinese/emoji documents, API key encryption loss on tenant settings update, vLLM streaming reasoning content propagation, and rerank empty passage errors


**v0.3.4 Highlights:**

- **IM Bot Integration**: WeCom, Feishu, and Slack IM channel support with WebSocket/Webhook modes, streaming, and knowledge base integration
- **Multimodal Image Support**: Image upload and multimodal image processing with enhanced session management
- **Manual Knowledge Download**: Download manual knowledge content as files with proper filename sanitization
- **NVIDIA Model API**: Support NVIDIA chat model API with custom endpoint and VLM model configuration
- **Weaviate Vector DB**: Added Weaviate as a new vector database backend for knowledge retrieval
- **AWS S3 Storage**: Integrated AWS S3 storage adapter with configuration UI and database migrations
- **AES-256-GCM Encryption**: API keys encrypted at rest with AES-256-GCM for enhanced security
- **Built-in MCP Service**: Built-in MCP service support for extending agent capabilities
- **Hybrid Search Optimization**: Grouped targets and reused query embeddings for better retrieval performance
- **Final Answer Tool**: New final_answer tool with agent duration tracking for improved agent workflows

**v0.3.3 Highlights:**

- **Parent-Child Chunking**: Hierarchical parent-child chunking strategy for enhanced context management and more accurate retrieval
- **Knowledge Base Pinning**: Pin frequently-used knowledge bases for quick access
- **Fallback Response**: Fallback response handling with UI indicators when no relevant results are found
- **Passage Cleaning for Rerank**: Passage cleaning for rerank model to improve relevance scoring accuracy
- **Storage Auto-Creation**: Storage engine connectivity check with auto-creation of buckets
- **Milvus Vector DB**: Added Milvus as a new vector database backend for knowledge retrieval

**v0.3.2 Highlights:**

- 🔍 **Knowledge Search**: New "Knowledge Search" entry point with semantic retrieval, supporting bringing search results directly into the conversation window
- ⚙️ **Parser & Storage Engine Configuration**: Configure document parser engines and storage engines for different sources in settings, with per-file-type parser selection in knowledge base
- 🖼️ **Image Rendering in Local Storage**: Support image rendering during conversations in local storage mode, with optimized streaming image placeholders
- 📄 **Document Preview**: Embedded document preview component for previewing user-uploaded original files
- 🎨 **UI Optimization**: Knowledge base, agent, and shared space list page interaction redesign
- 🗄️ **Milvus Support**: Added Milvus as a new vector database backend for knowledge retrieval
- 🌋 **Volcengine TOS**: Added Volcengine TOS object storage support
- 📊 **Mermaid Rendering**: Support mermaid diagram rendering in chat with fullscreen viewer, zoom, pan, toolbar and export
- 💬 **Batch Conversation Management**: Batch management and delete all sessions functionality
- 🔗 **Remote URL Knowledge**: Support creating knowledge entries from remote file URLs
- 🧠 **Memory Graph Preview**: Preview of user-level memory graph visualization
- 🔄 **Async Re-parse**: Async API for re-processing existing knowledge documents

**v0.3.0 Highlights:**

- 🏢 **Shared Space**: Shared space with member invitations, shared knowledge bases and agents across members, tenant-isolated retrieval
- 🧩 **Agent Skills**: Agent skills system with preloaded skills for smart-reasoning agent, sandboxed execution environment for security isolation
- 🤖 **Custom Agents**: Support for creating, configuring, and selecting custom agents with knowledge base selection modes (all/specified/disabled)
- 📊 **Data Analyst Agent**: Built-in Data Analyst agent with DataSchema tool for CSV/Excel analysis
- 🧠 **Thinking Mode**: Support thinking mode for LLM and agents, intelligent filtering of thinking content
- 🔍 **Web Search Providers**: Added Bing and Google search providers alongside DuckDuckGo
- 📋 **Enhanced FAQ**: Batch import dry run, similar questions, matched question in search results, large imports offloaded to object storage
- 🔑 **API Key Auth**: API Key authentication mechanism with Swagger documentation security
- 📎 **In-Input Selection**: Select knowledge bases and files directly in the input box with @mention display
- ☸️ **Helm Chart**: Complete Helm chart for Kubernetes deployment with Neo4j GraphRAG support
- 🌍 **i18n**: Added Korean (한국어) language support
- 🔒 **Security Hardening**: SSRF-safe HTTP client, enhanced SQL validation, MCP stdio transport security, sandbox-based execution
- ⚡ **Infrastructure**: Qdrant vector DB support, Redis ACL, configurable log level, Ollama embedding optimization, `DISABLE_REGISTRATION` control

**v0.2.0 Highlights:**

- 🤖 **Agent Mode**: New ReACT Agent mode that can call built-in tools, MCP tools, and web search, providing comprehensive summary reports through multiple iterations and reflection
- 📚 **Multi-Type Knowledge Bases**: Support for FAQ and document knowledge base types, with new features including folder import, URL import, tag management, and online entry
- ⚙️ **Conversation Strategy**: Support for configuring Agent models, normal mode models, retrieval thresholds, and Prompts, with precise control over multi-turn conversation behavior
- 🌐 **Web Search**: Support for extensible web search engines with built-in DuckDuckGo search engine
- 🔌 **MCP Tool Integration**: Support for extending Agent capabilities through MCP, with built-in uvx and npx launchers, supporting multiple transport methods
- 🎨 **New UI**: Optimized conversation interface with Agent mode/normal mode switching, tool call process display, and comprehensive knowledge base management interface upgrade
- ⚡ **Infrastructure Upgrade**: Introduced MQ async task management, support for automatic database migration, and fast development mode

</details>


## 📱 Interface Showcase

<table>
  <tr>
    <td colspan="2" align="center"><b>💬 Intelligent Q&A Conversation</b><br/><img src="./docs/images/qa.png" alt="Intelligent Q&A Conversation" width="100%"></td>
  </tr>
  <tr>
    <td width="50%" align="center"><b>📖 Wiki Browser</b><br/><img src="./docs/images/wiki-browser.png" alt="Wiki Browser" width="100%"></td>
    <td width="50%" align="center"><b>🕸️ Wiki Knowledge Graph</b><br/><img src="./docs/images/wiki-graph.png" alt="Wiki Knowledge Graph" width="100%"></td>
  </tr>
  <tr>
    <td width="50%" align="center"><b>🤖 Agent Mode · Tool Call Process</b><br/><img src="./docs/images/agent-qa.png" alt="Agent Mode Tool Call Process" width="100%"></td>
    <td width="50%" align="center"><b>⚙️ Conversation Settings</b><br/><img src="./docs/images/settings.png" alt="Conversation Settings" width="100%"></td>
  </tr>
  <tr>
    <td colspan="2" align="center"><b>🔭 Observability · Langfuse Tracing</b><br/><img src="./docs/images/langfuse.png" alt="Observability Langfuse Tracing" width="100%"></td>
  </tr>
</table>

## 🏗️ Architecture

![weknora-architecture.png](./docs/images/architecture.png)

Fully modular pipeline from document parsing, vectorization, and retrieval to LLM inference — every component is swappable and extensible. Supports local / private cloud deployment with full data sovereignty and a zero-barrier Web UI for quick onboarding.

## 🧩 Feature Overview

**Intelligent Conversation**

| Capability | Details |
|------------|---------|
| Intelligent Reasoning | ReACT progressive multi-step reasoning, autonomously orchestrating knowledge retrieval, MCP tools, and web search; custom agent support |
| Quick Q&A | RAG-based Q&A over knowledge bases for fast and accurate answers |
| Wiki Mode | Agent-driven auto-generation of structured, interlinked markdown Wiki pages from raw documents |
| Tool Calling | Built-in tools, MCP tools, web search |
| Conversation Strategy | Online Prompt editing, retrieval threshold tuning, multi-turn context awareness |
| Suggested Questions | Auto-generated question suggestions based on knowledge base content |

**Knowledge Management**

| Capability | Details |
|------------|---------|
| Knowledge Base Types | FAQ / Document / Wiki with folder import, URL import, tag management, and online entry |
| Data Source Import | Auto-sync from Feishu / Notion / Yuque (more data sources coming soon); incremental and full sync |
| Document Formats | PDF / Word / Txt / Markdown / HTML / Images / CSV / Excel / PPT / JSON |
| Retrieval Strategies | BM25 sparse / Dense retrieval / GraphRAG / parent-child chunking / multi-dimensional indexing |
| E2E Testing | Full-pipeline visualization with recall hit rate, BLEU / ROUGE metric evaluation |

**Integrations & Extensions**

| Capability | Details |
|------------|---------|
| LLMs | OpenAI / Azure OpenAI / DeepSeek / Qwen (Alibaba Cloud) / Zhipu / Hunyuan / Doubao (Volcengine) / Gemini / MiniMax / NVIDIA / Novita AI / SiliconFlow / OpenRouter / Ollama |
| Embeddings | Ollama / BGE / GTE / OpenAI-compatible APIs |
| Vector DBs | PostgreSQL (pgvector) / Elasticsearch / Milvus / Weaviate / Qdrant |
| Object Storage | Local / MinIO / AWS S3 / Volcengine TOS / Alibaba Cloud OSS |
| IM Channels | WeCom / Feishu / Slack / Telegram / DingTalk / Mattermost / WeChat |
| Web Search | DuckDuckGo / Bing / Google / Tavily / Baidu / Ollama |

**Platform**

| Capability | Details |
|------------|---------|
| Deployment | Local / Docker / Kubernetes (Helm) with private and offline support |
| UI | Web UI / RESTful API / Chrome Extension / WeChat Mini Program |
| Observability | Integrated Langfuse for ReAct loops, token tracking, tool calls, and pipeline tracing |
| Task Management | MQ async tasks, automatic database migration on version upgrade |
| Model Management | Centralized config, per-knowledge-base model selection, multi-tenant built-in model sharing, WeKnora Cloud hosted models and parsing |

## 🧩 Chrome Extension

[**WeKnora Chrome Extension**](https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd) lets you capture web content directly into your WeKnora knowledge base. Select text, images, or entire pages in the browser and save them as knowledge entries with one click — no copy-paste or file upload needed.


## 📱 WeChat Mini Program

The [WeKnora Mini Program](./miniprogram/README.md) provides a lightweight mobile client for configuring WeKnora API access, selecting knowledge bases, importing URLs, and asking knowledge chat from WeChat.


## 🦞 ClawHub Skill

[**WeKnora ClawHub Skill**](https://clawhub.ai/lyingbug/weknora) is a WeKnora skill published on the ClawHub platform. Once installed, it enables document import (file / URL / Markdown), hybrid search (vector + keyword) across knowledge bases, and knowledge entry management — all through the WeKnora REST API.

- **Document Import** — Upload files, import web pages, or write Markdown knowledge via the agent
- **Hybrid Search** — Search within or across knowledge bases with vector + keyword retrieval
- **Knowledge Management** — List, browse, edit, and delete knowledge entries programmatically

## ⌨️ Command-Line Interface

`weknora` is the official CLI for driving the API from a terminal or AI agent.
The command surface mirrors `gh` CLI's `<noun> <verb>` convention; output is
human-readable by default and switches to a stable JSON envelope with `--json`.

```bash
weknora auth login --host https://kb.example.com
weknora kb list
weknora link --kb my-knowledge-base    # bind the current directory
weknora doc upload notes.md
weknora chat "summarise the design doc"
```

See [`cli/README.md`](./cli/README.md) for install + 5-minute quickstart
and [`cli/AGENTS.md`](./cli/AGENTS.md) for the operational contract that
AI agents (Claude Code, Cursor, Aider, …) can rely on.

## 🚀 Getting Started

### 🛠 Prerequisites

- [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)
- [Git](https://git-scm.com/)

### 📦 Installation & Launch

```bash
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
cp .env.example .env   # Edit .env as needed, see comments in the file
docker compose up -d   # Start core services
```

Once started, visit **http://localhost** to get started.

> To use a local Ollama model, run `ollama serve > /dev/null 2>&1 &` first.

### 🔧 Optional Services (Docker Compose Profiles)

Add `--profile` flags to enable additional components. Multiple profiles can be combined:

| Profile | Description | Command |
|---------|-------------|---------|
| _(default)_ | Core services | `docker compose up -d` |
| `full` | All features | `docker compose --profile full up -d` |
| `neo4j` | Knowledge Graph (Neo4j) | `docker compose --profile neo4j up -d` |
| `minio` | Object Storage (MinIO) | `docker compose --profile minio up -d` |
| `langfuse` | Tracing (Langfuse) | `docker compose --profile langfuse up -d` |

Combine profiles: `docker compose --profile neo4j --profile minio up -d`

Stop services: `docker compose down`

### 🌐 Service URLs

| Service | URL |
|---------|-----|
| Web UI | `http://localhost` |
| Backend API | `http://localhost:8080` |
| Langfuse Tracing | `http://localhost:3000` |

## MCP Server

Please refer to the [MCP Configuration Guide](./mcp-server/MCP_CONFIG.md) for the necessary setup.

## 🔌 Using WeChat Dialog Open Platform

WeKnora serves as the core technology framework for the [WeChat Dialog Open Platform](https://chatbot.weixin.qq.com), providing a more convenient usage approach:

- **Zero-code Deployment**: Simply upload knowledge to quickly deploy intelligent Q&A services within the WeChat ecosystem, achieving an "ask and answer" experience
- **Efficient Question Management**: Support for categorized management of high-frequency questions, with rich data tools to ensure accurate, reliable, and easily maintainable answers
- **WeChat Ecosystem Integration**: Through the WeChat Dialog Open Platform, WeKnora's intelligent Q&A capabilities can be seamlessly integrated into WeChat Official Accounts, Mini Programs, and other WeChat scenarios, enhancing user interaction experiences



## 📘 API Reference

Troubleshooting FAQ: [Troubleshooting FAQ](./docs/QA.md)

Detailed API documentation is available at: [API Docs](./docs/api/README.md)

Product plans and upcoming features: [Roadmap](./docs/ROADMAP.md)

## 🧭 Developer Guide

### ⚡ Fast Development Mode (Recommended)

If you need to frequently modify code, **you don't need to rebuild Docker images every time**! Use fast development mode:

```bash
# Start infrastructure
make dev-start

# Start backend (new terminal)
make dev-app

# Start frontend (new terminal)
make dev-frontend
```

**Development Advantages:**
- ✅ Frontend modifications auto hot-reload (no restart needed)
- ✅ Backend modifications quick restart (5-10 seconds, supports Air hot-reload)
- ✅ No need to rebuild Docker images
- ✅ Support IDE breakpoint debugging

**Detailed Documentation:** [Development Environment Quick Start](./docs/开发指南.md)

### 📁 Directory Structure

```
WeKnora/
├── client/      # go client
├── cmd/         # Main entry point
├── config/      # Configuration files
├── docker/      # docker images files
├── docreader/   # Document parsing app
├── docs/        # Project documentation
├── frontend/    # Frontend app
├── internal/    # Core business logic
├── mcp-server/  # MCP server
├── migrations/  # DB migration scripts
└── scripts/     # Shell scripts
```

## 🤝 Contributing

Welcome to submit [Issues](https://github.com/Tencent/WeKnora/issues) or Pull Requests.

**Process:** Fork → Create branch → Commit changes → Open PR

**Standards:** Format code with `gofmt`, follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:` / `fix:` / `docs:` / `test:` / `refactor:`)

## 🔒 Security Notice

**Important:** Starting from v0.1.3, WeKnora includes login authentication functionality to enhance system security. For production deployments, we strongly recommend:

- Deploy WeKnora services in internal/private network environments rather than public internet
- Avoid exposing the service directly to public networks to prevent potential information leakage
- Configure proper firewall rules and access controls for your deployment environment
- Regularly update to the latest version for security patches and improvements

## 👥 Contributors

Thanks to these excellent contributors:

[![Contributors](https://contrib.rocks/image?repo=Tencent/WeKnora)](https://github.com/Tencent/WeKnora/graphs/contributors)

## 📄 License

This project is licensed under the [MIT License](./LICENSE).
You are free to use, modify, and distribute the code with proper attribution.

## 📈 Project Statistics

<a href="https://www.star-history.com/#Tencent/WeKnora&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Tencent/WeKnora&type=date&legend=top-left" />
 </picture>
</a>
