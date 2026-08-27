<p align="center">
  <picture>
    <img src="./docs/images/logo.png" alt="GRIDORA Logo" height="120"/>
  </picture>
</p>

<p align="center">
  <picture>
    <a href="https://trendshift.io/repositories/15289" target="_blank">
      <img src="https://trendshift.io/api/badge/repositories/15289" alt="Tencent/GRIDORA | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/>
    </a>
  </picture>
</p>
<p align="center">
    <a href="https://gridora.weixin.qq.com" target="_blank">
        <img alt="Official Website" src="https://img.shields.io/badge/Official Website-GRIDORA-4e6b99">
    </a>
    <a href="https://chatbot.weixin.qq.com" target="_blank">
        <img alt="WeChat Dialog Open Platform" src="https://img.shields.io/badge/WeChat Dialog Open Platform-5ac725">
    </a>
    <a href="https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd" target="_blank">
        <img alt="Chrome Extension" src="https://img.shields.io/badge/Chrome Extension-GRIDORA-4285F4">
    </a>
    <a href="https://clawhub.ai/lyingbug/gridora" target="_blank">
        <img alt="ClawHub Skill" src="https://img.shields.io/badge/ClawHub Skill-GRIDORA-ff6b35">
    </a>
    <a href="https://github.com/Tencent/GRIDORA/blob/main/LICENSE">
        <img src="https://img.shields.io/badge/License-MIT-ffffff?labelColor=d4eaf7&color=2e6cc4" alt="License">
    </a>
    <a href="./CHANGELOG.md">
        <img alt="Version" src="https://img.shields.io/badge/version-0.7.2-2e6cc4?labelColor=d4eaf7">
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

# 💡 GRIDORA — Turn Documents into Living Knowledge with RAG, Agents and Auto-Wiki

## 📌 Overview

[**GRIDORA**](https://gridora.weixin.qq.com) is an open-source, LLM-powered knowledge framework built for enterprise-grade document understanding, semantic retrieval, and autonomous reasoning.

It is organized around three core capabilities: **RAG-based Quick Q&A** for everyday lookups, a **ReAct Agent** that autonomously orchestrates retrieval, MCP tools and web search to handle complex multi-step tasks, and a brand-new **Wiki Mode** in which agents distill raw documents into a self-maintaining, interlinked markdown knowledge base with an interactive knowledge graph, complete with manual editing, revision history and one-click rollback. Knowledge curation is equally hands-on: a **tree-structured folder view** preserves the directory layout of uploads, and **chunk editing with revision history** lets retrieval chunks be edited, diffed and reverted like documents. Combined with multi-source ingestion (Feishu wiki / Feishu Drive / Notion / Yuque / RSS, and growing), **website embed widgets** for publishing agents to external sites, **scoped API keys with a principal model** for programmatic integrations, **multi-instance storage backends** per workspace for flexible data placement, 20+ LLM provider integrations, full Langfuse observability plus a **runtime task-queue dashboard with worker-pool governance**, **enterprise-ready multi-workspace RBAC** (4-tier role matrix + per-resource ownership + per-workspace audit log), and a fully self-hostable modular architecture, GRIDORA turns scattered documents into a queryable, reasoning-capable, continuously evolving knowledge asset.

The framework supports auto-syncing knowledge from Feishu, Notion, and Yuque (more data sources coming soon), handles 10+ document formats including PDF, Word, images, and Excel, and can serve Q&A directly through IM channels like WeCom, Feishu, Slack, and Telegram. It is compatible with major LLM providers including OpenAI, DeepSeek, Qwen (Alibaba Cloud), Zhipu, Hunyuan, Gemini, MiniMax, NVIDIA, and Ollama. Its fully modular design allows swapping LLMs, vector databases, and storage backends, with support for local and private cloud deployment ensuring complete data sovereignty. GRIDORA also integrates with **Langfuse** for comprehensive observability into agent reasoning, token usage, and pipeline tracing.


## ✨ Latest Updates

- **v0.7.2** — Launched the **official product documentation site** (VitePress; six sections, ~50 pages covering ~360 API endpoints and ~150 environment variables, with standalone Docker/Nginx deployment, quickstart sample data and a local MCP demo); **knowledge base folder tree** (upload paths stored as first-class data, browse/rename/re-file documents like a file manager); **chunk editing with revision history** (edit retrieval chunks in the UI, per-version diff and rollback, automatic reindexing, plus custom document metadata); **Wiki page revision history** (snapshots + line-level diff + one-click rollback + in-browser manual editing); **directly loadable file URLs** via `resource_urls=public` / `RESOURCE_URL_MODE` (third-party apps render images and files without a second authenticated proxy call); **Feishu Drive data source** and docx sync through the blocks API; batch document tagging; **MCP Server 1.1.x** (migrated to the mcp 2.x high-level API, official PyPI package `tencent-gridora-mcp`, new `create_knowledge_from_text` and `list_shared_knowledge_bases` for 29 tools total); AWS S3 default credential chain (IAM Role / IRSA); local HTML upload parsing; QQBot markdown replies; new PR CI checks for app / frontend / docreader / mcp-server. Plus large-scale router and `modelcontext` refactors, rerank and chunking quality work, and broad stability fixes. See [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.7.1** — New **Yunzhijia (云之家) IM integration** (WebSocket + image messages + markdown replies); **Volcengine rerank** provider (with request batching) and **Zhipu AI web search** provider; **platform-scoped API keys** for control-plane automation (tenant management, system settings, runtime queues, audit logs); **per-KB activity audit trail**; FAQ management enhancements (filtering, tagging, export, import tracking); **Langfuse OTLP/OTel tracing** migration with W3C traceparent propagation; chat header actions with one-click **Markdown export** and wiki tool results in the references drawer; prompt-cache observability; session channel governance (admin-scoped IM/embed/API sessions); resilient Feishu large-wiki sync; and removal of the legacy Neo4j conversation-memory dependency. Plus broad slug-integrity, SSRF-transport, and state-sync hardening. See [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.7.0** — Fine-grained **scoped API keys & principal model** (capability-level grants + per-KB restriction + API integration playground); **runtime task-queue observability dashboard & worker-pool governance** (per-stage pools + per-model concurrency governors + failed-task inspection/retry); **multi-instance storage backends** (multiple storage instances per workspace, per-KB binding, default instance); **session-scoped temporary attachments** (async image/doc parsing + combined limits); question & follow-up suggestions; stable resource registry with LLM-context alias compaction; `@Skill / @MCP` mentions with scoped agent runtime; mid-conversation MCP OAuth; QQBot & Lark (Feishu International) IM integration; Redis TLS; Requesty model provider + Keenable web search; tenantless provisioning & gated self-service workspaces; admin password reset; knowledge base duplicate flow; `gridora` CLI v0.10. Plus broad security hardening (SSRF, secret redaction, SQL validation, IDOR). See [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.6.3** — Website embed widget & Integrations Center (secure-mode token exchange + rate limits); chat experience overhaul (citation popovers, RAG pipeline progress, streaming markdown); document multi-tag & batch reparse; Wiki folders & hierarchy navigation; RSS data source; MCP OAuth2; EPUB / MHTML parsing; agent model-readiness checks; model test debugger; session source filter; workspace deletion UI. See [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.6.2** — Per-upload process configuration with upload-confirm dialog; document reparse with `process_config`; `gridora` CLI v0.9 (bundled Agent Skills, `session stop`, auth/profile harmonization); KB marquee multi-select; HNSW index for 1024-dim pgvector embeddings; chat resources store refactor; Langfuse-only tracing (Jaeger removed). See [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.6.1** — Document parsing trace timeline (Langfuse-style span tree with stage-by-stage progress + stop-parse); OpenSearch vector store driver; declarative built-in models via YAML; system admin & consolidated platform settings + audit log; new-user onboarding guide; settings UI redesign; `gridora` CLI v0.7 / v0.8 (agent-first wire contract, NDJSON, `--dry-run`); OpenDataLoader + PaddleOCR-VL parsers; MCP server multi-transport (stdio / SSE / HTTP); per-model thinking-mode config; Tencent LKEAP rerank + native Gemini embeddings + MiniMax-M3. See [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.6.0** — Workspace RBAC (4-tier role matrix `Owner` / `Admin` / `Contributor` / `Viewer` + per-KB ownership + per-workspace audit log), workspace member management & multi-workspace UX, self-service workspaces; `gridora` CLI v0.4 GA with `mcp serve`; KB retrieval fan-out across vector stores; AES-256-GCM credential encryption + docreader gRPC TLS + Token; Zhipu embedder + Huawei OBS; server-side user preferences; Go 1.26.0. See [`docs/RBAC说明.md`](./docs/RBAC说明.md) and [`CHANGELOG.md`](./CHANGELOG.md).
- **v0.5.2** — Wiki ingest scales to 40k-document KBs (task queue + DLQ); MCP human-in-the-loop tool approval; Anthropic / Apache Doris / Tencent VectorDB / KS3 / SearXNG backends; adaptive 3-tier chunking with live preview; global ⌘K command palette; Yuque connector + WeChat Mini Program; `gridora` CLI preview.
- **v0.5.1** — Knowledge-base batch management; workspace-wide IM channels overview; session search + user-scoped pinning; unified Model / Web Search / MCP settings cards; per-agent LLM timeout; desktop workspace switching.
- **v0.5.0** — Wiki Mode GA — agents auto-generate structured, interlinked Markdown wiki pages with a knowledge graph; wiki browser + visual graph in the UI.
- **v0.4.0** — GRIDORA Cloud (hosted LLM + parsing); Chrome Extension; ClawHub Skill; WeChat IM; attachment processing; Azure OpenAI / Alibaba OSS; Notion connector; Baidu + Ollama web search; VectorStore management.
- **v0.3.6** — ASR (audio); Feishu data-source auto-sync; OIDC; IM quote-reply context + thread-based sessions; document summarization; Tavily search; parallel tool calling; agent @mention scope restriction.
- **v0.3.5** — Telegram / DingTalk / Mattermost IM; IM slash commands + QA queue; suggested questions; VLM auto-describe MCP tool images; Novita AI; channel tracking.
- **v0.3.4** — WeCom / Feishu / Slack IM; multimodal image support; NVIDIA model API; Weaviate; AWS S3; AES-256-GCM API-key encryption; built-in MCP service; hybrid-search optimization; `final_answer` tool.
- **v0.3.3** — Parent-child chunking; KB pinning; fallback response; passage cleaning for rerank; storage auto-creation; Milvus.
- **v0.3.2** — Knowledge Search entry; per-source parser & storage engine config; image rendering in local storage; document preview; Volcengine TOS; Mermaid rendering; batch session management; memory graph preview.
- **v0.3.0** — Shared Space; Agent Skills + sandboxed execution; custom agents; Data Analyst agent; thinking mode; Bing / Google web search; API Key auth; Helm chart; Korean i18n; Qdrant.
- **v0.2.0** — Agent Mode (ReACT); multi-type knowledge bases (FAQ + document); conversation strategy config; DuckDuckGo web search; MCP tool integration; new UI with agent mode switching; MQ async task management.


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
    <td width="50%" align="center"><b>🕘 Wiki Page Revision History & Rollback</b><br/><img src="./docs/images/wiki-revision-history.png" alt="Wiki Page Revision History and Rollback" width="100%"></td>
    <td width="50%" align="center"><b>✂️ Chunk Editing & Revision History</b><br/><img src="./docs/images/kb-chunk-edit.png" alt="Chunk Editing and Revision History" width="100%"></td>
  </tr>
  <tr>
    <td width="50%" align="center"><b>📁 Folder Tree & Batch Operations</b><br/><img src="./docs/images/kb-document-list.png" alt="Knowledge Base Folder Tree and Batch Operations" width="100%"></td>
    <td width="50%" align="center"><b>🤖 Agent Mode · Tool Call Process</b><br/><img src="./docs/images/agent-qa.png" alt="Agent Mode Tool Call Process" width="100%"></td>
  </tr>
  <tr>
    <td colspan="2" align="center"><b>🔭 Observability · Langfuse Tracing</b><br/><img src="./docs/images/langfuse.png" alt="Observability Langfuse Tracing" width="100%"></td>
  </tr>
</table>

## 🏗️ Architecture

![gridora-architecture.png](./docs/images/architecture.png)

Fully modular pipeline from document parsing, vectorization, and retrieval to LLM inference — every component is swappable and extensible. Supports local / private cloud deployment with full data sovereignty and a zero-barrier Web UI for quick onboarding.

## 🧩 Feature Overview

**Intelligent Conversation**

| Capability | Details |
|------------|---------|
| Intelligent Reasoning | ReACT progressive multi-step reasoning, autonomously orchestrating knowledge retrieval, MCP tools, and web search |
| Quick Q&A | RAG-based Q&A over knowledge bases for fast and accurate answers |
| Wiki Mode | Agent-driven auto-generation of structured, interlinked markdown Wiki pages from raw documents; in-browser manual editing, page revision history, line-level diff and one-click rollback |
| Tool Calling | Built-in tools, MCP tools (incl. OAuth2 remote services, mid-conversation OAuth), web search; `@Skill / @MCP` mentions to scope the agent runtime per turn |
| Conversation Strategy | Online Prompt editing, retrieval threshold tuning, multi-turn context awareness, per-agent citation output toggle |
| Suggested Questions | Auto-generated question suggestions and after-answer follow-ups based on knowledge base content |
| Temporary Attachments | Session-scoped image / document uploads with async parsing for one-off Q&A, with a combined image + attachment limit |
| Citations & RAG Progress | Inline citation popovers and a references drawer (web / KB source distinction), shared markdown rendering, and stage-by-stage RAG pipeline progress in chat |
| Session Management | Filter and group sidebar sessions by source (Web / IM / Embed), with inline session-title rename |

**Knowledge Management**

| Capability | Details |
|------------|---------|
| Knowledge Base Types | FAQ / Document / Wiki with folder import, URL import, multi-tag management, and online entry |
| Folder Tree | Folder uploads keep their original directory structure, with a sidebar tree for browsing, folder rename, and re-filing documents into another folder |
| Chunk Editing & Revisions | Edit retrieval chunks directly in the UI with per-version snapshots, diff and one-click rollback, and automatic reindexing after an edit; generated questions can be added, edited, deleted and regenerated; custom document metadata supported |
| Per-Upload Process Config | Override parser, chunking, multimodal (VLM / ASR), graph extraction, and question generation per upload batch via upload-confirm dialog or `process_config` API; reparse with new settings |
| Batch Reparse | Re-queue parsing for multiple documents at once with optional per-batch `process_config` |
| Data Source Import | Auto-sync from Feishu wiki / Feishu Drive / Lark / Notion / Yuque / RSS feeds (more data sources coming soon); incremental and full sync |
| Document Formats | PDF / Word / Txt / Markdown / HTML / EPUB / MHTML / Images / CSV / Excel / PPT / JSON |
| Retrieval Strategies | BM25 sparse / Dense retrieval / GraphRAG / parent-child chunking / HNSW-accelerated pgvector (1024-dim) / multi-dimensional indexing |
| Batch Selection & Tagging | Marquee drag-select multiple documents in the KB list for batch reparse and batch tagging (common tags pre-selected) |
| E2E Testing | Full-pipeline visualization with recall hit rate, BLEU / ROUGE metric evaluation |

**Integrations & Extensions**

| Capability | Details |
|------------|---------|
| LLMs | OpenAI / Azure OpenAI / Anthropic (Claude) / DeepSeek / Qwen (Alibaba Cloud) / Zhipu / Hunyuan / Doubao (Volcengine) / Gemini / MiniMax / NVIDIA / Novita AI / SiliconFlow / OpenRouter / Requesty / Ollama |
| Embeddings | Ollama / BGE / GTE / Zhipu / OpenAI-compatible APIs |
| Vector DBs | PostgreSQL (pgvector) / Elasticsearch / OpenSearch / Milvus / Weaviate / Qdrant / Apache Doris / Tencent VectorDB |
| Object Storage | Local / MinIO / AWS S3 (IAM Role / IRSA default credential chain) / Volcengine TOS / Alibaba Cloud OSS / Kingsoft Cloud KS3 / Huawei Cloud OBS; **multiple storage instances per workspace** with per-KB binding and a default instance |
| IM Channels | WeCom / Feishu / Lark (Feishu International) / QQBot / Slack / Telegram / DingTalk / Mattermost / WeChat / Yunzhijia |
| Website Embed | Publish agents via embed widget with domain allowlists, rate limits, and secure-mode token exchange |
| Web Search | DuckDuckGo / Bing / Google / Tavily / Baidu / Ollama / SearXNG / Keenable / Zhipu AI |
| API Integration | Scoped API keys (capability-level grants + per-KB restriction + throttled last-used tracking) with an API integration playground; MCP OAuth and embed sessions isolated per principal; `resource_urls=public` returns directly loadable file/image URLs, removing the second authenticated proxy call |
| MCP Server | Official PyPI package `tencent-gridora-mcp` with 29 tools over stdio / SSE / HTTP transports |

**Platform**

| Capability | Details |
|------------|---------|
| Deployment | Local / Docker / Kubernetes (Helm) with private and offline support |
| UI | Web UI / RESTful API / CLI (`gridora`) / Chrome Extension / Website Embed Widget / WeChat Mini Program |
| Access Control | Workspace RBAC with 4-tier role matrix (Owner / Admin / Contributor / Viewer), per-KB resource ownership, per-workspace audit log, invite-only workspaces, tenantless provisioning & gated self-service workspace creation, admin password reset (session revocation), cross-workspace superuser, scoped API keys |
| Security | AES-256-GCM at-rest encryption for API keys and MCP / data-source credentials with graceful key rotation; gRPC TLS + Token between app and docreader; Redis TLS; SSRF-safe HTTP client (data sources, URL import, redirect chains); secret redaction in responses; sandbox isolation for agent skills |
| Observability | Integrated Langfuse (sole tracing backend) for ReAct loops, token tracking, tool calls, and pipeline tracing; built-in Langfuse-style document parsing trace timeline with stage-by-stage progress; system-admin runtime task-queue dashboard (queue depth, per-model concurrency, failed-task inspection & manual retry) |
| Task Management | MQ async tasks with per-stage worker-pool governance (core / post-process / enrichment / maintenance + elastic shared pool, plus an independent Wiki pool) and per-model background concurrency governors; automatic database migration on version upgrade |
| Model Management | Centralized config, declarative built-in models via YAML, per-knowledge-base model selection, per-model thinking-mode and embedding-dimension overrides, interactive model test debugger, multi-workspace built-in model sharing, GRIDORA Cloud hosted models and parsing |

## 🧩 Chrome Extension

[**GRIDORA Chrome Extension**](https://chromewebstore.google.com/detail/jpemjbopikggjlmikmclgbmkhhopjdgd) lets you capture web content directly into your GRIDORA knowledge base. Select text, images, or entire pages in the browser and save them as knowledge entries with one click — no copy-paste or file upload needed.


## 📱 WeChat Mini Program

The [GRIDORA Mini Program](./miniprogram/README.md) provides a lightweight mobile client for configuring GRIDORA API access, selecting knowledge bases, importing URLs, and asking knowledge chat from WeChat.


## 🦞 ClawHub Skill

[**GRIDORA ClawHub Skill**](https://clawhub.ai/lyingbug/gridora) is a GRIDORA skill published on the ClawHub platform. Once installed, it enables document import (file / URL / Markdown), hybrid search (vector + keyword) across knowledge bases, and knowledge entry management — all through the GRIDORA REST API.

- **Document Import** — Upload files, import web pages, or write Markdown knowledge via the agent
- **Hybrid Search** — Search within or across knowledge bases with vector + keyword retrieval
- **Knowledge Management** — List, browse, edit, and delete knowledge entries programmatically

## 🐋 DeepSeek Harness Plugin

[**`@wxg-prc-cpg/dsh-gridora`**](./packages/dsh-gridora/README.md) is the official [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`) plugin. The harness ships no retrieval, embedding or knowledge-base capability of its own, so the plugin gives a coding agent your documents: `dsh plugin --profile web add @wxg-prc-cpg/dsh-gridora`, point it at a deployment, and four read-only tools appear in the agent's tool set.

- **`gridora_search`** — hybrid retrieval returning source passages verbatim, each with a reusable `knowledge_id`
- **`gridora_read_document`** — one document's passages reassembled in order, with paging
- **`gridora_ask`** — GRIDORA's own composed answer with citations, over the RAG or the ReAct pipeline
- **`gridora_list_knowledge_bases`** — knowledge base names and ids, so the agent can scope its own search

## ⌨️ Command-Line Interface

`gridora` is the official CLI for driving the API from a terminal or an AI
agent. It is **agent-first**: every command emits a stable JSON envelope by
default (with typed error codes mapped to exit codes), and `--format text`
renders for humans. It also serves a curated MCP tool surface
(`gridora mcp serve`) and ships bundled Agent Skills.

```bash
gridora profile add prod --host https://kb.example.com --use
gridora auth login
gridora kb list
gridora link --kb my-knowledge-base    # bind the current directory
gridora doc upload notes.md
gridora chat "summarise the design doc"
```

For headless / CI use, set `WEKNORA_API_KEY` + `WEKNORA_HOST` and skip
`auth login` entirely — no credentials written to disk.

See [`cli/README.md`](./cli/README.md) for install + 5-minute quickstart and
[`cli/AGENTS.md`](./cli/AGENTS.md) for the operational contract AI agents rely on.

## 🚀 Getting Started

### 🛠 Prerequisites

- [Docker](https://www.docker.com/) & [Docker Compose](https://docs.docker.com/compose/)
- [Git](https://git-scm.com/)

### 📦 Installation & Launch

```bash
git clone https://github.com/Tencent/GRIDORA.git
cd GRIDORA
cp .env.example .env   # Edit .env as needed, see comments in the file
docker compose pull     # Pull the latest images
docker compose up -d    # Start core services
```

Once started, visit **http://localhost** to get started.

> To use a local Ollama model, run `ollama serve > /dev/null 2>&1 &` first.

### 🔄 Upgrading

If you already have GRIDORA running and downloaded a newer release:

```bash
# Set WEKNORA_VERSION in .env to the target release (e.g. 0.7.0), or keep latest
docker compose pull     # Pull images matching WEKNORA_VERSION
docker compose up -d    # Recreate containers with new images
```

> `docker compose up -d` alone reuses locally cached images and may leave the UI version out of sync with the release you downloaded.

### 🔧 Optional Services (Docker Compose Profiles)

Add `--profile` flags to enable additional components. Multiple profiles can be combined:

| Profile | Description | Command |
|---------|-------------|---------|
| _(default)_ | Core services | `docker compose pull && docker compose up -d` |
| `full` | All features | `docker compose --profile full pull && docker compose --profile full up -d` |
| `neo4j` | Knowledge Graph (Neo4j) | `docker compose --profile neo4j pull && docker compose --profile neo4j up -d` |
| `minio` | Object Storage (MinIO) | `docker compose --profile minio pull && docker compose --profile minio up -d` |
| `langfuse` | Tracing (Langfuse) | `docker compose --profile langfuse pull && docker compose --profile langfuse up -d` |

Combine profiles: `docker compose --profile neo4j --profile minio pull && docker compose --profile neo4j --profile minio up -d`

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

GRIDORA serves as the core technology framework for the [WeChat Dialog Open Platform](https://chatbot.weixin.qq.com), providing a more convenient usage approach:

- **Zero-code Deployment**: Simply upload knowledge to quickly deploy intelligent Q&A services within the WeChat ecosystem, achieving an "ask and answer" experience
- **Efficient Question Management**: Support for categorized management of high-frequency questions, with rich data tools to ensure accurate, reliable, and easily maintainable answers
- **WeChat Ecosystem Integration**: Through the WeChat Dialog Open Platform, GRIDORA's intelligent Q&A capabilities can be seamlessly integrated into WeChat Official Accounts, Mini Programs, and other WeChat scenarios, enhancing user interaction experiences



## 📘 API Reference

**Official product documentation**: [`website-docs/`](./website-docs/README.md) — the complete documentation set organized as Getting Started → Architecture → Features → API → Clients → Development, covering ~360 API endpoints, ~150 environment variables, and 9 extension points. The directory is also a VitePress site: run `cd website-docs && npm install && npm run dev` to preview locally, or deploy it standalone with the `Dockerfile` inside.

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


## 🤝 Contributing

Welcome to submit [Issues](https://github.com/Tencent/GRIDORA/issues) or Pull Requests.

**Process:** Fork → Create branch → Commit changes → Open PR

**Standards:** Format code with `gofmt`, follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:` / `fix:` / `docs:` / `test:` / `refactor:`)

### Validation

For a focused PR, validate the changed scope first:

```bash
git fetch origin main
git diff --check origin/main...HEAD
golangci-lint run --new-from-rev=origin/main ./...
go test ./path/to/changed/package -count=1
```

Run `gofmt` on changed Go files before committing. For frontend changes, run the relevant tests from `frontend/` and use `npm run type-check` when the change affects TypeScript or Vue components.

The full maintainer gate remains:

```bash
make fmt
make lint
make test
```

`make fmt` formats the entire Go repository, so run it only with a clean worktree and review the resulting diff. Some full-suite tests require local infrastructure or service configuration. If a full check fails for an unrelated baseline or environment reason, include the exact command and failure in the PR while still providing passing targeted tests for your change.

## 🔒 Security Notice

**Important:** Starting from v0.1.3, GRIDORA includes login authentication functionality to enhance system security. For production deployments, we strongly recommend:

- Deploy GRIDORA services in internal/private network environments rather than public internet
- Avoid exposing the service directly to public networks to prevent potential information leakage
- Configure proper firewall rules and access controls for your deployment environment
- Regularly update to the latest version for security patches and improvements

## 👥 Contributors

Thanks to these excellent contributors:

[![Contributors](https://contrib.rocks/image?repo=Tencent/GRIDORA)](https://github.com/Tencent/GRIDORA/graphs/contributors)

## 📄 License

This project is licensed under the [MIT License](./LICENSE).
You are free to use, modify, and distribute the code with proper attribution.

## 📈 Project Statistics

<a href="https://www.star-history.com/#Tencent/GRIDORA&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=Tencent/GRIDORA&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=Tencent/GRIDORA&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=Tencent/GRIDORA&type=date&legend=top-left" />
 </picture>
</a>
