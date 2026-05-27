#!/usr/bin/env python3
"""
WeKnora MCP Server

A Model Context Protocol server that provides access to the WeKnora knowledge management API.
"""

import json
import logging
import os
from typing import Any, Dict

import mcp.server.stdio
import mcp.types as types
import requests
from mcp.server import NotificationOptions, Server
from mcp.server.models import InitializationOptions
from requests.exceptions import RequestException

# Set up logging configuration for the MCP server
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Configuration - Load from environment variables with defaults
WEKNORA_BASE_URL = os.getenv("WEKNORA_BASE_URL", "http://localhost:8080/api/v1")
WEKNORA_API_KEY = os.getenv("WEKNORA_API_KEY", "")


class WeKnoraClient:
    """Client for interacting with WeKnora API"""

    def __init__(self, base_url: str, api_key: str):
        """Initialize the WeKnora API client with base URL and authentication"""
        self.base_url = base_url
        self.api_key = api_key
        # Create a persistent session for connection pooling and performance
        self.session = requests.Session()
        # Set default headers for all requests
        self.session.headers.update(
            {
                "X-API-Key": api_key,  # API key for authentication
                "Content-Type": "application/json",  # Default content type
            }
        )

    def _request(self, method: str, endpoint: str, **kwargs) -> Dict[str, Any]:
        """Make a request to the WeKnora API

        Args:
            method: HTTP method (GET, POST, PUT, DELETE)
            endpoint: API endpoint path
            **kwargs: Additional arguments to pass to requests

        Returns:
            JSON response as dictionary
        """
        url = f"{self.base_url}{endpoint}"
        try:
            # Execute HTTP request with the specified method
            response = self.session.request(method, url, **kwargs)
            # Raise exception for HTTP error status codes (4xx, 5xx)
            response.raise_for_status()
            # Parse and return JSON response
            return response.json()
        except RequestException as e:
            logger.error(f"API request failed: {e}")
            raise

    # Tenant Management - Methods for managing multi-tenant configurations
    def create_tenant(
        self, name: str, description: str, business: str, retriever_engines: Dict
    ) -> Dict:
        """Create a new tenant with specified configuration"""
        data = {
            "name": name,
            "description": description,
            "business": business,
            "retriever_engines": retriever_engines,  # Configuration for search engines
        }
        return self._request("POST", "/tenants", json=data)

    def get_tenant(self, tenant_id: str) -> Dict:
        """Get tenant information"""
        return self._request("GET", f"/tenants/{tenant_id}")

    def list_tenants(self) -> Dict:
        """List all tenants"""
        return self._request("GET", "/tenants")

    # Knowledge Base Management - Methods for managing knowledge bases
    def create_knowledge_base(self, name: str, description: str, config: Dict) -> Dict:
        """Create a new knowledge base with chunking and model configuration"""
        data = {
            "name": name,
            "description": description,
            **config,  # Merge additional configuration (chunking, models, etc.)
        }
        return self._request("POST", "/knowledge-bases", json=data)

    def list_knowledge_bases(self) -> Dict:
        """List all knowledge bases"""
        return self._request("GET", "/knowledge-bases")

    def get_knowledge_base(self, kb_id: str) -> Dict:
        """Get knowledge base details"""
        return self._request("GET", f"/knowledge-bases/{kb_id}")

    def update_knowledge_base(self, kb_id: str, updates: Dict) -> Dict:
        """Update knowledge base"""
        return self._request("PUT", f"/knowledge-bases/{kb_id}", json=updates)

    def delete_knowledge_base(self, kb_id: str) -> Dict:
        """Delete knowledge base"""
        return self._request("DELETE", f"/knowledge-bases/{kb_id}")

    def hybrid_search(self, kb_id: str, query: str, config: Dict) -> Dict:
        """Perform hybrid search combining vector and keyword search"""
        data = {
            "query_text": query,
            **config,  # Include thresholds and match count
        }
        return self._request(
            "GET", f"/knowledge-bases/{kb_id}/hybrid-search", json=data
        )

    # Knowledge Management - Methods for creating and managing knowledge entries
    def create_knowledge_from_file(
        self, kb_id: str, file_path: str, enable_multimodel: bool = True
    ) -> Dict:
        """Create knowledge from a local file with optional multimodal processing"""
        with open(file_path, "rb") as f:
            files = {"file": f}
            data = {"enable_multimodel": str(enable_multimodel).lower()}
            # Temporarily remove Content-Type header for multipart/form-data request
            # (requests will set it automatically with boundary)
            headers = self.session.headers.copy()
            del headers["Content-Type"]
            # Use requests.post directly instead of session to avoid header conflicts
            response = requests.post(
                f"{self.base_url}/knowledge-bases/{kb_id}/knowledge/file",
                headers=headers,
                files=files,
                data=data,
            )
            response.raise_for_status()
            return response.json()

    def create_knowledge_from_url(
        self, kb_id: str, url: str, enable_multimodel: bool = True
    ) -> Dict:
        """Create knowledge from a web URL with optional multimodal processing"""
        data = {
            "url": url,  # Web URL to fetch and process
            "enable_multimodel": enable_multimodel,  # Enable image/multimodal extraction
        }
        return self._request(
            "POST", f"/knowledge-bases/{kb_id}/knowledge/url", json=data
        )

    def list_knowledge(self, kb_id: str, page: int = 1, page_size: int = 20) -> Dict:
        """List knowledge in a knowledge base"""
        params = {"page": page, "page_size": page_size}
        return self._request(
            "GET", f"/knowledge-bases/{kb_id}/knowledge", params=params
        )

    def get_knowledge(self, knowledge_id: str) -> Dict:
        """Get knowledge details"""
        return self._request("GET", f"/knowledge/{knowledge_id}")

    def delete_knowledge(self, knowledge_id: str) -> Dict:
        """Delete knowledge"""
        return self._request("DELETE", f"/knowledge/{knowledge_id}")

    # Model Management - Methods for managing AI models (LLM, Embedding, Rerank)
    def create_model(
        self,
        name: str,
        model_type: str,
        source: str,
        description: str,
        parameters: Dict,
        is_default: bool = False,
    ) -> Dict:
        """Create a new AI model configuration"""
        data = {
            "name": name,
            "type": model_type,  # KnowledgeQA, Embedding, or Rerank
            "source": source,  # local, openai, etc.
            "description": description,
            "parameters": parameters,  # API keys, base URLs, etc.
            "is_default": is_default,  # Set as default model for this type
        }
        return self._request("POST", "/models", json=data)

    def list_models(self) -> Dict:
        """List all models"""
        return self._request("GET", "/models")

    def get_model(self, model_id: str) -> Dict:
        """Get model details"""
        return self._request("GET", f"/models/{model_id}")

    # Session Management - Methods for managing chat sessions
    def create_session(self, kb_id: str, strategy: Dict) -> Dict:
        """Create a new chat session with conversation strategy"""
        data = {
            "knowledge_base_id": kb_id,  # Knowledge base to query
            "session_strategy": strategy,  # Conversation settings (max rounds, rewrite, etc.)
        }
        return self._request("POST", "/sessions", json=data)

    def get_session(self, session_id: str) -> Dict:
        """Get session details"""
        return self._request("GET", f"/sessions/{session_id}")

    def list_sessions(self, page: int = 1, page_size: int = 20) -> Dict:
        """List sessions"""
        params = {"page": page, "page_size": page_size}
        return self._request("GET", "/sessions", params=params)

    def delete_session(self, session_id: str) -> Dict:
        """Delete session"""
        return self._request("DELETE", f"/sessions/{session_id}")

    # Chat Functionality - Methods for conversational interactions
    def chat(self, session_id: str, query: str) -> Dict:
        """Send a chat message and get AI response"""
        data = {"query": query}
        # Note: The actual API returns Server-Sent Events (SSE) stream
        # This simplified version returns the complete response
        return self._request("POST", f"/knowledge-chat/{session_id}", json=data)

    # Chunk Management - Methods for managing knowledge chunks (text segments)
    def list_chunks(
        self, knowledge_id: str, page: int = 1, page_size: int = 20
    ) -> Dict:
        """List text chunks of a knowledge entry with pagination"""
        params = {"page": page, "page_size": page_size}
        return self._request("GET", f"/chunks/{knowledge_id}", params=params)

    def delete_chunk(self, knowledge_id: str, chunk_id: str) -> Dict:
        """Delete a chunk"""
        return self._request("DELETE", f"/chunks/{knowledge_id}/{chunk_id}")

    # Wiki Read-Only - Methods for querying LLM-generated wiki pages
    def wiki_search(self, kb_id: str, query: str, limit: int = 10) -> Dict:
        """Search wiki pages by full-text query"""
        return self._request(
            "GET",
            f"/knowledgebase/{kb_id}/wiki/search",
            params={"q": query, "limit": limit},
        )

    def wiki_read_page(self, kb_id: str, slug: str) -> Dict:
        """Read a wiki page by slug, returns full markdown + metadata + links"""
        return self._request("GET", f"/knowledgebase/{kb_id}/wiki/pages/{slug}")

    def wiki_index_view(self, kb_id: str, limit: int = 50) -> Dict:
        """Get structured wiki index with per-type directory groups"""
        return self._request(
            "GET",
            f"/knowledgebase/{kb_id}/wiki/index",
            params={"limit": limit},
        )


# Initialize MCP server instance
app = Server("weknora-server")
# Initialize WeKnora API client with configuration
client = WeKnoraClient(WEKNORA_BASE_URL, WEKNORA_API_KEY)


# Tool definitions - Register all available tools for the MCP protocol
@app.list_tools()
async def handle_list_tools() -> list[types.Tool]:
    """List all available WeKnora tools with their schemas"""
    return [
        # Tenant Management
        types.Tool(
            name="create_tenant",
            description="Create a new tenant in WeKnora",
            inputSchema={
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
        types.Tool(
            name="list_tenants",
            description="List all tenants",
            inputSchema={"type": "object", "properties": {}},
        ),
        # Knowledge Base Management
        types.Tool(
            name="create_knowledge_base",
            description="Create a new knowledge base",
            inputSchema={
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
        types.Tool(
            name="list_knowledge_bases",
            description="List all knowledge bases",
            inputSchema={"type": "object", "properties": {}},
        ),
        types.Tool(
            name="get_knowledge_base",
            description="Get knowledge base details",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"}
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="delete_knowledge_base",
            description="Delete a knowledge base",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"}
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="hybrid_search",
            description="Perform hybrid search in knowledge base",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "Knowledge base ID"},
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
        types.Tool(
            name="create_knowledge_from_file",
            description="Create knowledge from a local file on the server filesystem",
            inputSchema={
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
        types.Tool(
            name="create_knowledge_from_url",
            description="Create knowledge from URL",
            inputSchema={
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
        types.Tool(
            name="list_knowledge",
            description="List knowledge in a knowledge base",
            inputSchema={
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
        types.Tool(
            name="get_knowledge",
            description="Get knowledge details",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"}
                },
                "required": ["knowledge_id"],
            },
        ),
        types.Tool(
            name="delete_knowledge",
            description="Delete knowledge",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"}
                },
                "required": ["knowledge_id"],
            },
        ),
        # Model Management
        types.Tool(
            name="create_model",
            description="Create a new model",
            inputSchema={
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
        types.Tool(
            name="list_models",
            description="List all models",
            inputSchema={"type": "object", "properties": {}},
        ),
        types.Tool(
            name="get_model",
            description="Get model details",
            inputSchema={
                "type": "object",
                "properties": {
                    "model_id": {"type": "string", "description": "Model ID"}
                },
                "required": ["model_id"],
            },
        ),
        # Session Management
        types.Tool(
            name="create_session",
            description="Create a new chat session",
            inputSchema={
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
                        "description": "Fallback response",
                        "default": "Sorry, I cannot answer this question.",
                    },
                    "summary_model_id": {
                        "type": "string",
                        "description": "Summary model ID",
                    },
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="get_session",
            description="Get session details",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"}
                },
                "required": ["session_id"],
            },
        ),
        types.Tool(
            name="list_sessions",
            description="List chat sessions",
            inputSchema={
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
        types.Tool(
            name="delete_session",
            description="Delete a session",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"}
                },
                "required": ["session_id"],
            },
        ),
        # Chat Functionality
        types.Tool(
            name="chat",
            description="Send a chat message to a session",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "Session ID"},
                    "query": {"type": "string", "description": "User query"},
                },
                "required": ["session_id", "query"],
            },
        ),
        # Chunk Management
        types.Tool(
            name="list_chunks",
            description="List chunks of knowledge",
            inputSchema={
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
        types.Tool(
            name="delete_chunk",
            description="Delete a chunk",
            inputSchema={
                "type": "object",
                "properties": {
                    "knowledge_id": {"type": "string", "description": "Knowledge ID"},
                    "chunk_id": {"type": "string", "description": "Chunk ID"},
                },
                "required": ["knowledge_id", "chunk_id"],
            },
        ),
        # Wiki Read-Only - Tools for querying LLM-generated wiki pages
        types.Tool(
            name="wiki_search",
            description="Search wiki pages by full-text query. Returns matching wiki pages with title, slug, summary, and content snippets.",
            inputSchema={
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
        types.Tool(
            name="wiki_read_page",
            description="Read a wiki page by its slug. Returns full markdown content, metadata, inbound/outbound links, and source references.",
            inputSchema={
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
        types.Tool(
            name="wiki_index_view",
            description="Get a structured wiki index with per-type directory groups. Returns an overview of all wiki pages organized by type (entity, concept, summary, etc.).",
            inputSchema={
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


@app.call_tool()
async def handle_call_tool(
    name: str, arguments: dict | None
) -> list[types.TextContent | types.ImageContent | types.EmbeddedResource]:
    """Handle tool execution requests from MCP clients

    Args:
        name: Name of the tool to execute
        arguments: Tool arguments as dictionary

    Returns:
        List of content items (text, image, or embedded resources)
    """

    try:
        # Use empty dict if no arguments provided
        args = arguments or {}

        # Tenant Management - Route tenant-related operations
        if name == "create_tenant":
            result = client.create_tenant(
                args["name"],
                args["description"],
                args["business"],
                # Default to postgres-based keyword and vector search if not specified
                args.get(
                    "retriever_engines",
                    {
                        "engines": [
                            {
                                "retriever_type": "keywords",
                                "retriever_engine_type": "postgres",
                            },
                            {
                                "retriever_type": "vector",
                                "retriever_engine_type": "postgres",
                            },
                        ]
                    },
                ),
            )
        elif name == "list_tenants":
            result = client.list_tenants()

        # Knowledge Base Management - Route knowledge base operations
        elif name == "create_knowledge_base":
            # Build configuration with defaults for chunking and models
            config = {
                "chunking_config": args.get(
                    "chunking_config",
                    {
                        "chunk_size": 1000,  # Default chunk size in characters
                        "chunk_overlap": 200,  # Default overlap between chunks
                        "separators": ["."],  # Default text separators
                        "enable_multimodal": True,  # Enable image processing by default
                    },
                ),
                "embedding_model_id": args.get("embedding_model_id", ""),
                "summary_model_id": args.get("summary_model_id", ""),
            }
            result = client.create_knowledge_base(
                args["name"], args["description"], config
            )
        elif name == "list_knowledge_bases":
            result = client.list_knowledge_bases()
        elif name == "get_knowledge_base":
            result = client.get_knowledge_base(args["kb_id"])
        elif name == "delete_knowledge_base":
            result = client.delete_knowledge_base(args["kb_id"])
        elif name == "hybrid_search":
            # Configure hybrid search with thresholds and result count
            config = {
                "vector_threshold": args.get(
                    "vector_threshold", 0.5
                ),  # Minimum similarity score
                "keyword_threshold": args.get(
                    "keyword_threshold", 0.3
                ),  # Minimum keyword match score
                "match_count": args.get(
                    "match_count", 5
                ),  # Number of results to return
            }
            result = client.hybrid_search(args["kb_id"], args["query"], config)

        # Knowledge Management
        elif name == "create_knowledge_from_file":
            result = client.create_knowledge_from_file(
                args["kb_id"], args["file_path"], args.get("enable_multimodel", True)
            )
        elif name == "create_knowledge_from_url":
            result = client.create_knowledge_from_url(
                args["kb_id"], args["url"], args.get("enable_multimodel", True)
            )
        elif name == "list_knowledge":
            result = client.list_knowledge(
                args["kb_id"], args.get("page", 1), args.get("page_size", 20)
            )
        elif name == "get_knowledge":
            result = client.get_knowledge(args["knowledge_id"])
        elif name == "delete_knowledge":
            result = client.delete_knowledge(args["knowledge_id"])

        # Model Management - Route model configuration operations
        elif name == "create_model":
            # Build model parameters (API credentials, endpoints, etc.)
            parameters = {
                "base_url": args.get("base_url", ""),  # Model API endpoint
                "api_key": args.get("api_key", ""),  # Model API key
            }
            result = client.create_model(
                args["name"],
                args["type"],
                args.get("source", "local"),
                args["description"],
                parameters,
                args.get("is_default", False),
            )
        elif name == "list_models":
            result = client.list_models()
        elif name == "get_model":
            result = client.get_model(args["model_id"])

        # Session Management - Route chat session operations
        elif name == "create_session":
            # Build session strategy with conversation settings
            strategy = {
                "max_rounds": args.get("max_rounds", 5),  # Maximum conversation turns
                "enable_rewrite": args.get(
                    "enable_rewrite", True
                ),  # Enable query rewriting
                "fallback_strategy": "FIXED_RESPONSE",  # Strategy when no answer found
                "fallback_response": args.get(
                    "fallback_response", "Sorry, I cannot answer this question."
                ),
                "embedding_top_k": 10,  # Number of chunks to retrieve
                "keyword_threshold": 0.5,  # Keyword match threshold
                "vector_threshold": 0.7,  # Vector similarity threshold
                "summary_model_id": args.get(
                    "summary_model_id", ""
                ),  # Model for summarization
            }
            result = client.create_session(args["kb_id"], strategy)
        elif name == "get_session":
            result = client.get_session(args["session_id"])
        elif name == "list_sessions":
            result = client.list_sessions(
                args.get("page", 1), args.get("page_size", 20)
            )
        elif name == "delete_session":
            result = client.delete_session(args["session_id"])

        # Chat Functionality
        elif name == "chat":
            result = client.chat(args["session_id"], args["query"])

        # Chunk Management
        elif name == "list_chunks":
            result = client.list_chunks(
                args["knowledge_id"], args.get("page", 1), args.get("page_size", 20)
            )
        elif name == "delete_chunk":
            result = client.delete_chunk(args["knowledge_id"], args["chunk_id"])

        # Wiki Read-Only - Route wiki query operations
        elif name == "wiki_search":
            result = client.wiki_search(
                args["kb_id"], args["query"], args.get("limit", 10)
            )
        elif name == "wiki_read_page":
            result = client.wiki_read_page(args["kb_id"], args["slug"])
        elif name == "wiki_index_view":
            result = client.wiki_index_view(
                args["kb_id"], args.get("limit", 50)
            )

        else:
            # Handle unknown tool names
            return [types.TextContent(type="text", text=f"Unknown tool: {name}")]

        # Return successful result as formatted JSON
        return [
            types.TextContent(
                type="text", text=json.dumps(result, indent=2, ensure_ascii=False)
            )
        ]

    except Exception as e:
        # Log and return error message
        logger.error(f"Tool execution failed: {e}")
        return [
            types.TextContent(type="text", text=f"Error executing {name}: {str(e)}")
        ]


async def run():
    """Run the MCP server using stdio transport"""
    # Create stdio streams for communication with MCP client
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        # Run the server with initialization options
        await app.run(
            read_stream,
            write_stream,
            InitializationOptions(
                server_name="weknora-server",
                server_version="1.0.0",
                capabilities=app.get_capabilities(
                    notification_options=NotificationOptions(),
                    experimental_capabilities={},
                ),
            ),
        )


def main():
    """Main entry point for console_scripts"""
    import asyncio

    # Run the async server
    asyncio.run(run())


if __name__ == "__main__":
    main()
