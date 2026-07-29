"""MCP tool schemas exposed by the WeKnora server."""

from __future__ import annotations

from mcp.types import Tool


def _tool(name: str, description: str, input_schema: dict) -> Tool:
    return Tool(name=name, description=description, inputSchema=input_schema)


def build_tools() -> list[Tool]:
    return [

        # Tenant Management
_tool(
            name="create_tenant",
            description="Create a new tenant in WeKnora",
            input_schema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "Tenant name"},
                    "description": {
                        "type": "string",
                        "description": "Tenant description",
                    },
                    "business": {"type": "string", "description": "Business type"},
                    "retriever_engines": {
                        "type": "object",
                        "description": "Retriever engine configuration",
                        "properties": {
                            "engines": {
                                "type": "array",
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "retriever_type": {"type": "string"},
                                        "retriever_engine_type": {"type": "string"},
                                    },
                                },
                            }
                        },
                    },
                },
                "required": ["name", "description", "business"],
            },
        ),
_tool(
            name="list_tenants",
            description="List all tenants",
            input_schema={"type": "object", "properties": {}},
        ),
        # Knowledge Base Management
_tool(
            name="create_knowledge_base",
            description="Create a new knowledge base",
            input_schema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "Knowledge base name"},
                    "description": {
                        "type": "string",
                        "description": "Knowledge base description",
                    },
                    "embedding_model_id": {
                        "type": "string",
                        "description": "Embedding model ID",
                    },
                    "summary_model_id": {
                        "type": "string",
                        "description": "Summary model ID",
                    },
                },
                "required": ["name", "description"],
            },
        ),
_tool(
            name="list_knowledge_bases",
            description="List all knowledge bases",
            input_schema={"type": "object", "properties": {}},
        ),
_tool(
            name="get_knowledge_base",
            description="Get knowledge base details",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"}
                },
                "required": ["kb_id"],
            },
        ),
_tool(
            name="delete_knowledge_base",
            description="Delete a knowledge base",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"}
                },
                "required": ["kb_id"],
            },
        ),
_tool(
            name="hybrid_search",
            description="Perform hybrid search in knowledge base",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {
                        "type": "string",
                        "description": "Knowledge base UUID (e.g. 'a1b2c3d4-e5f6-7890-abcd-ef1234567890') OR name (e.g. 'my-knowledge-base'). Use list_knowledge_bases to discover available knowledge bases.",
                    },
                    "query": {"type": "string", "description": "Search query"},
                    "vector_threshold": {
                        "type": "number",
                        "description": "Vector similarity threshold",
                        "default": 0.5,
                    },
                    "keyword_threshold": {
                        "type": "number",
                        "description": "Keyword match threshold",
                        "default": 0.3,
                    },
                    "match_count": {
                        "type": "integer",
                        "description": "Number of results to return",
                        "default": 5,
                    },
                },
                "required": ["kb_id", "query"],
            },
        ),
        # Knowledge Management
_tool(
            name="create_knowledge_from_file",
            description="Create knowledge from a local file on the server filesystem",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "file_path": {
                        "type": "string",
                        "description": "Absolute path to the local file on the server",
                    },
                    "enable_multimodel": {
                        "type": "boolean",
                        "description": "Enable multimodal processing",
                        "default": True,
                    },
                },
                "required": ["kb_id", "file_path"],
            },
        ),
_tool(
            name="create_knowledge_from_url",
            description="Create knowledge from URL",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "url": {
                        "type": "string",
                        "description": "URL to create knowledge from",
                    },
                    "enable_multimodel": {
                        "type": "boolean",
                        "description": "Enable multimodal processing",
                        "default": True,
                    },
                },
                "required": ["kb_id", "url"],
            },
        ),
_tool(
            name="list_knowledge",
            description="List knowledge in a knowledge base",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "page": {
                        "type": "integer",
                        "description": "Page number",
                        "default": 1,
                    },
                    "page_size": {
                        "type": "integer",
                        "description": "Page size",
                        "default": 20,
                    },
                },
                "required": ["kb_id"],
            },
        ),
_tool(
            name="get_knowledge",
            description="Get knowledge details",
            input_schema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"}
                },
                "required": ["knowledge_id"],
            },
        ),
_tool(
            name="delete_knowledge",
            description="Delete knowledge",
            input_schema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"}
                },
                "required": ["knowledge_id"],
            },
        ),
        # Model Management
_tool(
            name="create_model",
            description="Create a new model",
            input_schema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "Model name"},
                    "type": {
                        "type": "string",
                        "description": "Model type (KnowledgeQA, Embedding, Rerank)",
                    },
                    "source": {
                        "type": "string",
                        "description": "Model source",
                        "default": "local",
                    },
                    "description": {
                        "type": "string",
                        "description": "Model description",
                    },
                    "base_url": {
                        "type": "string",
                        "description": "Model API base URL",
                        "default": "",
                    },
                    "api_key": {
                        "type": "string",
                        "description": "Model API key",
                        "default": "",
                    },
                    "is_default": {
                        "type": "boolean",
                        "description": "Set as default model",
                        "default": False,
                    },
                },
                "required": ["name", "type", "description"],
            },
        ),
_tool(
            name="list_models",
            description="List all models",
            input_schema={"type": "object", "properties": {}},
        ),
_tool(
            name="get_model",
            description="Get model details",
            input_schema={
                "type": "object",
                "properties": {
                    "model_id": {"type": "string", "description": "Model ID"}
                },
                "required": ["model_id"],
            },
        ),
        # Session Management
_tool(
            name="create_session",
            description="Create a new chat session with conversation strategy for a knowledge base",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "max_rounds": {
                        "type": "integer",
                        "description": "Maximum conversation rounds",
                        "default": 5,
                    },
                    "enable_rewrite": {
                        "type": "boolean",
                        "description": "Enable query rewriting",
                        "default": True,
                    },
                    "fallback_response": {
                        "type": "string",
                        "description": "Fallback response when no answer found",
                        "default": "Sorry, I cannot answer this question.",
                    },
                    "summary_model_id": {"type": "string", "description": "Model ID for response summarization (optional)"},
                    "title": {"type": "string", "description": "Session title (optional)"},
                    "description": {"type": "string", "description": "Session description (optional)"},
                },
                "required": ["kb_id"],
            },
        ),
_tool(
            name="get_session",
            description="Get session details",
            input_schema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"}
                },
                "required": ["session_id"],
            },
        ),
_tool(
            name="list_sessions",
            description="List chat sessions",
            input_schema={
                "type": "object",
                "properties": {
                    "page": {
                        "type": "integer",
                        "description": "Page number",
                        "default": 1,
                    },
                    "page_size": {
                        "type": "integer",
                        "description": "Page size",
                        "default": 20,
                    },
                },
            },
        ),
_tool(
            name="delete_session",
            description="Delete a session",
            input_schema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"}
                },
                "required": ["session_id"],
            },
        ),
        # Chat Functionality
_tool(
            name="chat",
            description=(
                "RAG pipeline chat: retrieve relevant chunks from knowledge bases, then summarise with LLM. "
                "ALWAYS provide knowledge_base_ids (names like 'my-knowledge-base' or UUIDs) so retrieval can run — "
                "without them the answer is based on LLM knowledge only. "
                "Use list_knowledge_bases to discover available knowledge bases. "
                "For multi-step reasoning or tool-calling use agent_chat instead."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID (from create_session or list_sessions)"},
                    "query": {"type": "string", "description": "User query"},
                    "knowledge_base_ids": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Knowledge base names OR UUIDs to search. Strongly recommended for RAG — without them the answer falls back to LLM knowledge only. E.g. ['my-knowledge-base'] or ['a1b2c3d4-...']. Use list_knowledge_bases to find them.",
                    },
                    "web_search_enabled": {"type": "boolean", "description": "Enable web search alongside KB retrieval.", "default": False},
                },
                "required": ["session_id", "query"],
            },
        ),
_tool(
            name="agent_chat",
            description=(
                "Agentic pipeline chat: the agent autonomously calls tools (knowledge_search, web_search, SQL, etc.) "
                "to answer the query. Use this for complex multi-step questions or comparative analysis. "
                "REQUIRED: agent_id (name or UUID) — use list_agents to discover agents. "
                "IMPORTANT: many agents have KBSelectionMode=none and NO built-in knowledge bases. "
                "In that case you MUST pass knowledge_base_ids, otherwise the agent will fail with "
                "'no search targets available'. "
                "Use get_agent to inspect an agent's kb_selection_mode and knowledge_bases before calling. "
                "If kb_selection_mode is 'none' or 'selected' with an empty list, always provide knowledge_base_ids."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID (from create_session or list_sessions)"},
                    "query": {"type": "string", "description": "User query"},
                    "agent_id": {
                        "type": "string",
                        "description": "REQUIRED. Custom agent UUID or name. Use list_agents to discover agents. Use get_agent to check its kb_selection_mode.",
                    },
                    "knowledge_base_ids": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Names or UUIDs of knowledge bases to search. REQUIRED when the agent's kb_selection_mode is 'none' or 'selected' with no built-in KBs. Use list_knowledge_bases to find them.",
                    },
                    "web_search_enabled": {"type": "boolean", "description": "Enable web search.", "default": False},
                },
                "required": ["session_id", "query", "agent_id"],
            },
        ),
_tool(
            name="list_agents",
            description="List all custom agents available to the current tenant. Use this to discover agent IDs, names, and their KB selection mode before calling agent_chat.",
            input_schema={
                "type": "object",
                "properties": {
                    "page": {"type": "integer", "description": "Page number", "default": 1},
                    "page_size": {"type": "integer", "description": "Page size", "default": 50},
                },
                "required": [],
            },
        ),
_tool(
            name="get_agent",
            description=(
                "Get full configuration of a single agent by UUID or name. "
                "Check kb_selection_mode and knowledge_bases fields: "
                "if kb_selection_mode is 'none' or 'selected' with an empty knowledge_bases list, "
                "you MUST pass knowledge_base_ids when calling agent_chat."
            ),
            input_schema={
                "type": "object",
                "properties": {
                    "agent_id": {"type": "string", "description": "Agent UUID or name"},
                },
                "required": ["agent_id"],
            },
        ),
        # Chunk Management
_tool(
            name="list_chunks",
            description="List chunks of knowledge",
            input_schema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"},
                    "page": {
                        "type": "integer",
                        "description": "Page number",
                        "default": 1,
                    },
                    "page_size": {
                        "type": "integer",
                        "description": "Page size",
                        "default": 20,
                    },
                },
                "required": ["knowledge_id"],
            },
        ),
_tool(
            name="delete_chunk",
            description="Delete a chunk",
            input_schema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"},
                    "chunk_id": {"type": "string", "description": "Chunk ID"},
                },
                "required": ["knowledge_id", "chunk_id"],
            },
        ),
        # Wiki Read-Only - Tools for querying LLM-generated wiki pages
_tool(
            name="wiki_search",
            description="Search wiki pages by full-text query. Returns matching wiki pages with title, slug, summary, and content snippets.",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "query": {"type": "string", "description": "Search query text"},
                    "limit": {
                        "type": "integer",
                        "description": "Maximum number of results to return",
                        "default": 10,
                    },
                },
                "required": ["kb_id", "query"],
            },
        ),
_tool(
            name="wiki_read_page",
            description="Read a wiki page by its slug. Returns full markdown content, metadata, inbound/outbound links, and source references.",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "slug": {
                        "type": "string",
                        "description": "Page slug (e.g. 'entity/acme-corp', 'concept/rag')",
                    },
                },
                "required": ["kb_id", "slug"],
            },
        ),
_tool(
            name="wiki_index_view",
            description="Get a structured wiki index with per-type directory groups. Returns an overview of all wiki pages organized by type (entity, concept, summary, etc.).",
            input_schema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
                    "limit": {
                        "type": "integer",
                        "description": "Maximum items per type group",
                        "default": 50,
                    },
                },
                "required": ["kb_id"],
            },
        ),
    ]


TOOLS = build_tools()
