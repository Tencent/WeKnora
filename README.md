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
        <img alt="官方网站" src="https://img.shields.io/badge/官方网站-WeKnora-4e6b99">
    </a>
    <a href="https://chatbot.weixin.qq.com" target="_blank">
        <img alt="微信对话开放平台" src="https://img.shields.io/badge/微信对话开放平台-5ac725">
    </a>
    <a href="https://github.com/Tencent/WeKnora/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="Version" src="https://img.shields.io/badge/version-0.3.6-2e6cc4?labelColor=d4eaf7">
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

# 💡 WeKnora - LLM-Powered Document Understanding & Retrieval Framework

## 📌 Overview

[**WeKnora**](https://weknora.weixin.qq.com) is an LLM-powered intelligent knowledge management and Q&A framework built for enterprise-grade document understanding and semantic retrieval.

WeKnora offers two Q&A modes — **Quick Q&A** and **Intelligent Reasoning**. Quick Q&A uses a **RAG (Retrieval-Augmented Generation)** pipeline to rapidly retrieve relevant chunks and generate answers, ideal for everyday knowledge queries. Intelligent Reasoning is powered by a **ReACT Agent** engine that employs a **progressive strategy** to autonomously orchestrate knowledge retrieval, MCP tools, and web search, iteratively reasoning and reflecting to arrive at a final conclusion — suited for multi-source synthesis and complex tasks. Custom agents are also supported, allowing flexible configuration of dedicated knowledge bases, tool sets, and system prompts. Choose the right mode for the task, balancing response speed with reasoning depth.

The framework supports auto-syncing knowledge from Feishu (more data sources coming soon), handles 10+ document formats including PDF, Word, images, and Excel, and can serve Q&A directly through IM channels like WeCom, Feishu, Slack, and Telegram. It is compatible with major LLM providers including OpenAI, DeepSeek, Qwen (Alibaba Cloud), Zhipu, Hunyuan, Gemini, MiniMax, NVIDIA, and Ollama. Its fully modular design allows swapping LLMs, vector databases, and storage backends, with support for local and private cloud deployment ensuring complete data sovereignty.

**Website:** https://weknora.weixin.qq.com

## ✨ Latest Updates

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

<details>
<summary><b>Earlier Releases</b></summary>

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


## 🏗️ Architecture

![weknora-architecture.png](./docs/images/architecture.png)

Fully modular pipeline from document parsing, vectorization, and retrieval to LLM inference — every component is swappable and extensible. Supports local / private cloud deployment with full data sovereignty and a zero-barrier Web UI for quick onboarding.

## 🧩 Feature Overview

**🤖 Intelligent Conversation**

| Capability | Details |
|------------|---------|
| Intelligent Reasoning | ReACT progressive multi-step reasoning, autonomously orchestrating knowledge retrieval, MCP tools, and web search; custom agent support |
| Quick Q&A | RAG-based Q&A over knowledge bases for fast and accurate answers |
| Tool Calling | Built-in tools, MCP tools, web search |
| Conversation Strategy | Online Prompt editing, retrieval threshold tuning, multi-turn context awareness |
| Suggested Questions | Auto-generated question suggestions based on knowledge base content |

**📚 Knowledge Management**

| Capability | Details |
|------------|---------|
| Knowledge Base Types | FAQ / Document with folder import, URL import, tag management, and online entry |
| Data Source Import | Auto-sync from Feishu (more data sources coming soon); incremental and full sync |
| Document Formats | PDF / Word / Txt / Markdown / HTML / Images / CSV / Excel / PPT / JSON |
| Retrieval Strategies | BM25 sparse / Dense retrieval / GraphRAG / parent-child chunking / multi-dimensional indexing |
| E2E Testing | Full-pipeline visualization with recall hit rate, BLEU / ROUGE metric evaluation |

**🔌 Integrations & Extensions**

| Capability | Details |
|------------|---------|
| LLMs | OpenAI / DeepSeek / Qwen (Alibaba Cloud) / Zhipu / Hunyuan / Doubao (Volcengine) / Gemini / MiniMax / NVIDIA / Novita AI / SiliconFlow / OpenRouter / Ollama |
| Embeddings | Ollama / BGE / GTE / OpenAI-compatible APIs |
| Vector DBs | PostgreSQL (pgvector) / Elasticsearch / Milvus / Weaviate / Qdrant |
| Object Storage | Local / MinIO / AWS S3 / Volcengine TOS |
| IM Channels | WeCom / Feishu / Slack / Telegram / DingTalk / Mattermost |
| Web Search | DuckDuckGo / Bing / Google / Tavily |

**🛡️ Platform**

| Capability | Details |
|------------|---------|
| Deployment | Local / Docker / Kubernetes (Helm) with private and offline support |
| UI | Web UI / RESTful API / Chrome Extension |
| Task Management | MQ async tasks, automatic database migration on version upgrade |
| Model Management | Centralized config, per-knowledge-base model selection, multi-tenant built-in model sharing |

## 🚀 Getting Started

### 🛠 Prerequisites

Make sure the following tools are installed on your system:

* [Docker](https://www.docker.com/)
* [Docker Compose](https://docs.docker.com/compose/)
* [Git](https://git-scm.com/)

### 📦 Installation

#### ① Clone the repository

```bash
# Clone the main repository
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora
```

#### ② Configure environment variables

```bash
# Copy example env file
cp .env.example .env

# Edit .env and set required values
# All variables are documented in the .env.example comments
```

#### ③ Start the core services

#### Start Ollama separately (Optional)

If you configured a local Ollama model in `.env`, start the Ollama service separately:

```bash
ollama serve > /dev/null 2>&1 &
```

#### Activate different combinations of features

- Minimum core services
```bash
docker compose up -d
```

- All features enabled
```bash
docker compose --profile full up -d
```

- Tracing logs required
```bash
docker compose --profile jaeger up -d
```

- Neo4j knowledge graph required
```bash
docker compose --profile neo4j up -d
```

- Minio file storage service required
```bash
docker compose --profile minio up -d
```

- Multiple options combination
```bash
docker compose --profile neo4j --profile minio up -d
```

#### ④ Stop the services

```bash
docker compose down
```

### 🌐 Access Services

Once started, services will be available at:

* Web UI: `http://localhost`
* Backend API: `http://localhost:8080`
* Jaeger Tracing: `http://localhost:16686`

## 📱 Interface Showcase

<table>
  <tr>
    <td colspan="2"><b>Intelligent Q&A Conversation</b><br/><img src="./docs/images/qa.png" alt="Intelligent Q&A Conversation"></td>
  </tr>
  <tr>
    <td colspan="2"><b>Agent Mode Tool Call Process</b><br/><img src="./docs/images/agent-qa.png" alt="Agent Mode Tool Call Process"></td>
  </tr>
    <tr>
    <td><b>Knowledge Base Management</b><br/><img src="./docs/images/knowledgebases.png" alt="Knowledge Base Management"></td>
    <td><b>Conversation Settings</b><br/><img src="./docs/images/settings.png" alt="Conversation Settings"></td>
  </tr>
</table>


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

We welcome community contributions! For suggestions, bugs, or feature requests, please submit an [Issue](https://github.com/Tencent/WeKnora/issues) or directly create a Pull Request.

### 🎯 How to Contribute

- 🐛 **Bug Fixes**: Discover and fix system defects
- ✨ **New Features**: Propose and implement new capabilities
- 📚 **Documentation**: Improve project documentation
- 🧪 **Test Cases**: Write unit and integration tests
- 🎨 **UI/UX Enhancements**: Improve user interface and experience

### 📋 Contribution Process

1. **Fork the project** to your GitHub account
2. **Create a feature branch** `git checkout -b feature/amazing-feature`
3. **Commit changes** `git commit -m 'Add amazing feature'`
4. **Push branch** `git push origin feature/amazing-feature`
5. **Create a Pull Request** with detailed description of changes

### 🎨 Code Standards

- Follow [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- Format code using `gofmt`
- Add necessary unit tests
- Update relevant documentation

### 📝 Commit Guidelines

Use [Conventional Commits](https://www.conventionalcommits.org/) standard:

```
feat: Add document batch upload functionality
fix: Resolve vector retrieval precision issue
docs: Update API documentation
test: Add retrieval engine test cases
refactor: Restructure document parsing module
```

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
