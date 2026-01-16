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
        <img alt="Version" src="https://img.shields.io/badge/version-0.2.10-2e6cc4?labelColor=d4eaf7">
    </a>
</p>

<p align="center">
| <b>English</b> | <a href="./README_CN.md"><b>简体中文</b></a> | <a href="./README_JA.md"><b>日本語</b></a> |
</p>

<p align="center">
  <h4 align="center">

  [Overview](#-overview) • [Architecture](#-architecture) • [Key Features](#-key-features) • [Getting Started](#-getting-started) • [API Reference](#-api-reference) • [Developer Guide](#-developer-guide)
  
  </h4>
</p>

# 💡 WeKnora - LLM-Powered Document Understanding & Retrieval Framework

## 📌 Overview

[**WeKnora**](https://weknora.weixin.qq.com) is an LLM-powered framework designed for deep document understanding and semantic retrieval, especially for handling complex, heterogeneous documents. 

It adopts a modular architecture that combines multimodal preprocessing, semantic vector indexing, intelligent retrieval, and large language model inference. At its core, WeKnora follows the **RAG (Retrieval-Augmented Generation)** paradigm, enabling high-quality, context-aware answers by combining relevant document chunks with model reasoning.

**Website:** https://weknora.weixin.qq.com

## ✨ Latest Updates

**v0.2.0 Highlights:**

- 🤖 **Agent Mode**: New ReACT Agent mode that can call built-in tools, MCP tools, and web search, providing comprehensive summary reports through multiple iterations and reflection
- 📚 **Multi-Type Knowledge Bases**: Support for FAQ and document knowledge base types, with new features including folder import, URL import, tag management, and online entry
- ⚙️ **Conversation Strategy**: Support for configuring Agent models, normal mode models, retrieval thresholds, and Prompts, with precise control over multi-turn conversation behavior
- 🌐 **Web Search**: Support for extensible web search engines with built-in DuckDuckGo search engine
- 🔌 **MCP Tool Integration**: Support for extending Agent capabilities through MCP, with built-in uvx and npx launchers, supporting multiple transport methods
- 🎨 **New UI**: Optimized conversation interface with Agent mode/normal mode switching, tool call process display, and comprehensive knowledge base management interface upgrade
- ⚡ **Infrastructure Upgrade**: Introduced MQ async task management, support for automatic database migration, and fast development mode

## 🔒 Security Notice

**Important:** Starting from v0.1.3, WeKnora includes login authentication functionality to enhance system security. For production deployments, we strongly recommend:

- Deploy WeKnora services in internal/private network environments rather than public internet
- Avoid exposing the service directly to public networks to prevent potential information leakage
- Configure proper firewall rules and access controls for your deployment environment
- Regularly update to the latest version for security patches and improvements

## 🏗️ Architecture

![weknora-architecture.png](./docs/images/architecture.png)

WeKnora employs a modern modular design to build a complete document understanding and retrieval pipeline. The system primarily includes document parsing, vector processing, retrieval engine, and large model inference as core modules, with each component being flexibly configurable and extendable.

## 🎯 Key Features

- **🤖 Agent Mode**: Support for ReACT Agent mode that can use built-in tools to retrieve knowledge bases, MCP tools, and web search tools to access external services, providing comprehensive summary reports through multiple iterations and reflection
- **🔍 Precise Understanding**: Structured content extraction from PDFs, Word documents, images and more into unified semantic views
- **🧠 Intelligent Reasoning**: Leverages LLMs to understand document context and user intent for accurate Q&A and multi-turn conversations
- **📚 Multi-Type Knowledge Bases**: Support for FAQ and document knowledge base types, with folder import, URL import, tag management, and online entry capabilities
- **🔧 Flexible Extension**: All components from parsing and embedding to retrieval and generation are decoupled for easy customization
- **⚡ Efficient Retrieval**: Hybrid retrieval strategies combining keywords, vectors, and knowledge graphs, with cross-knowledge base retrieval support
- **🌐 Web Search**: Support for extensible web search engines with built-in DuckDuckGo search engine
- **🔌 MCP Tool Integration**: Support for extending Agent capabilities through MCP, with built-in uvx and npx launchers, supporting multiple transport methods
- **⚙️ Conversation Strategy**: Support for configuring Agent models, normal mode models, retrieval thresholds, and Prompts, with precise control over multi-turn conversation behavior
- **🎯 User-Friendly**: Intuitive web interface and standardized APIs for zero technical barriers
- **🔒 Secure & Controlled**: Support for local deployment and private cloud, ensuring complete data sovereignty

## 📊 Application Scenarios

| Scenario | Applications | Core Value |
|---------|----------|----------|
| **Enterprise Knowledge Management** | Internal document retrieval, policy Q&A, operation manual search | Improve knowledge discovery efficiency, reduce training costs |
| **Academic Research Analysis** | Paper retrieval, research report analysis, scholarly material organization | Accelerate literature review, assist research decisions |
| **Product Technical Support** | Product manual Q&A, technical documentation search, troubleshooting | Enhance customer service quality, reduce support burden |
| **Legal & Compliance Review** | Contract clause retrieval, regulatory policy search, case analysis | Improve compliance efficiency, reduce legal risks |
| **Medical Knowledge Assistance** | Medical literature retrieval, treatment guideline search, case analysis | Support clinical decisions, improve diagnosis quality |

## 🧩 Feature Matrix

| Module | Support                                                                        | Description                                                                                                                                                        |
|---------|--------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Agent Mode | ✅ ReACT Agent Mode                                                             | Support for using built-in tools to retrieve knowledge bases, MCP tools, and web search, with cross-knowledge base retrieval and multiple iterations               |
| Knowledge Base Types | ✅ FAQ / Document                                                               | Support for creating FAQ and document knowledge base types, with folder import, URL import, tag management, and online entry                                       |
| Document Formats | ✅ PDF / Word / Txt / Markdown / Images (with OCR / Caption)                    | Support for structured and unstructured documents with text extraction from images                                                                                 |
| Model Management | ✅ Centralized configuration, built-in model sharing                            | Centralized model configuration with model selection in knowledge base settings, support for multi-tenant shared built-in models                                   |
| Embedding Models | ✅ Local models, BGE / GTE APIs, etc.                                           | Customizable embedding models, compatible with local deployment and cloud vector generation APIs                                                                   |
| Vector DB Integration | ✅ PostgreSQL (pgvector), Elasticsearch                                         | Support for mainstream vector index backends, flexible switching for different retrieval scenarios                                                                 |
| Retrieval Strategies | ✅ BM25 / Dense Retrieval / GraphRAG                                            | Support for sparse/dense recall and knowledge graph-enhanced retrieval with customizable retrieve-rerank-generate pipelines                                        |
| LLM Integration | ✅ Support for Qwen, DeepSeek, etc., with thinking/non-thinking mode switching  | Compatible with local models (e.g., via Ollama) or external API services with flexible inference configuration                                                     |
| Conversation Strategy | ✅ Agent models, normal mode models, retrieval thresholds, Prompt configuration | Support for configuring Agent models, normal mode models, retrieval thresholds, online Prompt configuration, precise control over multi-turn conversation behavior |
| Web Search | ✅ Extensible search engines, DuckDuckGo / Google                               | Support for extensible web search engines with built-in DuckDuckGo search engine                                                                                   |
| MCP Tools | ✅ uvx, npx launchers, Stdio/HTTP Streamable/SSE                                | Support for extending Agent capabilities through MCP, with built-in uvx and npx launchers, supporting three transport methods                                      |
| QA Capabilities | ✅ Context-aware, multi-turn dialogue, prompt templates                         | Support for complex semantic modeling, instruction control and chain-of-thought Q&A with configurable prompts and context windows                                  |
| E2E Testing | ✅ Retrieval+generation process visualization and metric evaluation             | End-to-end testing tools for evaluating recall hit rates, answer coverage, BLEU/ROUGE and other metrics                                                            |
| Deployment Modes | ✅ Support for local deployment / Docker images                                 | Meets private, offline deployment and flexible operation requirements, with fast development mode support                                                          |
| User Interfaces | ✅ Web UI + RESTful API                                                         | Interactive interface and standard API endpoints, with Agent mode/normal mode switching and tool call process display                                              |
| Task Management | ✅ MQ async tasks, automatic database migration                                 | MQ-based async task state maintenance, support for automatic database schema and data migration during version upgrades                                            |

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

#### ③ Start the services (include Ollama)

Check the images that need to be started in the .env file.

```bash
./scripts/start_all.sh
```

or

```bash
make start-all
```

#### ③.0 Start ollama services (Optional)

```bash
ollama serve > /dev/null 2>&1 &
```

#### ③.1 Activate different combinations of features

- Minimum core services
```bash
docker compose up -d
```

- All features enabled
```bash
docker-compose --profile full up -d
```

- Tracing logs required
```bash
docker-compose --profile jaeger up -d
```

- Neo4j knowledge graph required
```bash
docker-compose --profile neo4j up -d
```

- Minio file storage service required
```bash
docker-compose --profile minio up -d
```

- Multiple options combination
```bash
docker-compose --profile neo4j --profile minio up -d
```

#### ④ Stop the services

```bash
./scripts/start_all.sh --stop
# Or
make stop-all
```

### 🌐 Access Services

Once started, services will be available at:

* Web UI: `http://localhost`
* Backend API: `http://localhost:8080`
* Jaeger Tracing: `http://localhost:16686`

### 🔌 Using WeChat Dialog Open Platform

WeKnora serves as the core technology framework for the [WeChat Dialog Open Platform](https://chatbot.weixin.qq.com), providing a more convenient usage approach:

- **Zero-code Deployment**: Simply upload knowledge to quickly deploy intelligent Q&A services within the WeChat ecosystem, achieving an "ask and answer" experience
- **Efficient Question Management**: Support for categorized management of high-frequency questions, with rich data tools to ensure accurate, reliable, and easily maintainable answers
- **WeChat Ecosystem Integration**: Through the WeChat Dialog Open Platform, WeKnora's intelligent Q&A capabilities can be seamlessly integrated into WeChat Official Accounts, Mini Programs, and other WeChat scenarios, enhancing user interaction experiences

### 🔗 Access WeKnora via MCP Server

#### 1️⃣ Clone the repository
```
git clone https://github.com/Tencent/WeKnora
```

#### 2️⃣ Configure MCP Server
> It is recommended to directly refer to the [MCP Configuration Guide](./mcp-server/MCP_CONFIG.md) for configuration.

Configure the MCP client to connect to the server:
```json
{
  "mcpServers": {
    "weknora": {
      "args": [
        "path/to/WeKnora/mcp-server/run_server.py"
      ],
      "command": "python",
      "env":{
        "WEKNORA_API_KEY":"Enter your WeKnora instance, open developer tools, check the request header x-api-key starting with sk",
        "WEKNORA_BASE_URL":"http(s)://your-weknora-address/api/v1"
      }
    }
  }
}
```

Run directly using stdio command:
```
pip install weknora-mcp-server
python -m weknora-mcp-server
```

## 🔧 Initialization Configuration Guide

To help users quickly configure various models and reduce trial-and-error costs, we've improved the original configuration file initialization method by adding a Web UI interface for model configuration. Before using, please ensure the code is updated to the latest version. The specific steps are as follows:
If this is your first time using this project, you can skip steps ①② and go directly to steps ③④.

### ① Stop the services

```bash
./scripts/start_all.sh --stop
```

### ② Clear existing data tables (recommended when no important data exists)

```bash
make clean-db
```

### ③ Compile and start services

```bash
./scripts/start_all.sh
```

### ④ Access Web UI

http://localhost

On your first visit, you will be automatically redirected to the registration/login page. After completing registration, please create a new knowledge base and finish the relevant settings on its configuration page.

## 📱 Interface Showcase

### Web UI Interface

<table>
  <tr>
    <td><b>Knowledge Base Management</b><br/><img src="./docs/images/knowledgebases.png" alt="Knowledge Base Management"></td>
    <td><b>Conversation Settings</b><br/><img src="./docs/images/settings.png" alt="Conversation Settings"></td>
  </tr>
  <tr>
    <td colspan="2"><b>Agent Mode Tool Call Process</b><br/><img src="./docs/images/agent-qa.png" alt="Agent Mode Tool Call Process"></td>
  </tr>
</table>

**Knowledge Base Management:** Support for creating FAQ and document knowledge base types, with multiple import methods including drag-and-drop, folder import, and URL import. Automatically identifies document structures and extracts core knowledge to establish indexes. Supports tag management and online entry. The system clearly displays processing progress and document status, achieving efficient knowledge base management.

**Agent Mode:** Support for ReACT Agent mode that can use built-in tools to retrieve knowledge bases, call user-configured MCP tools and web search tools to access external services, providing comprehensive summary reports through multiple iterations and reflection. Supports cross-knowledge base retrieval, allowing selection of multiple knowledge bases for simultaneous retrieval.

**Conversation Strategy:** Support for configuring Agent models, normal mode models, retrieval thresholds, and online Prompt configuration, with precise control over multi-turn conversation behavior and retrieval execution methods. The conversation input box supports Agent mode/normal mode switching, enabling/disabling web search, and selecting conversation models.

### Document Knowledge Graph

WeKnora supports transforming documents into knowledge graphs, displaying the relationships between different sections of the documents. Once the knowledge graph feature is enabled, the system analyzes and constructs an internal semantic association network that not only helps users understand document content but also provides structured support for indexing and retrieval, enhancing the relevance and breadth of search results.

For detailed configuration, please refer to the [Knowledge Graph Configuration Guide](./docs/KnowledgeGraph.md).

### MCP Server

Please refer to the [MCP Configuration Guide](./mcp-server/MCP_CONFIG.md) for the necessary setup.

## 📘 API Reference

Troubleshooting FAQ: [Troubleshooting FAQ](./docs/QA.md)

Detailed API documentation is available at: [API Docs](./docs/api/README.md)

## 🧭 Developer Guide

### ⚡ Fast Development Mode (Recommended)

If you need to frequently modify code, **you don't need to rebuild Docker images every time**! Use fast development mode:

```bash
# Method 1: Using Make commands (Recommended)
make dev-start      # Start infrastructure
make dev-app        # Start backend (new terminal)
make dev-frontend   # Start frontend (new terminal)

# Method 2: One-click start
./scripts/quick-dev.sh

# Method 3: Using scripts
./scripts/dev.sh start     # Start infrastructure
./scripts/dev.sh app       # Start backend (new terminal)
./scripts/dev.sh frontend  # Start frontend (new terminal)
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
