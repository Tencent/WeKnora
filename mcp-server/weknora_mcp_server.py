#!/usr/bin/env python3
"""
WeKnora MCP Server

A Model Context Protocol server that provides access to the WeKnora knowledge management API.
"""

import argparse
import asyncio
import functools
import json
import logging
import os
import re
from typing import Any, Dict

import urllib3
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
# Chat SSE read timeout in seconds. LLM responses can be slow; default 300s.
try:
    WEKNORA_CHAT_TIMEOUT = int(os.getenv("WEKNORA_CHAT_TIMEOUT", "300"))
except ValueError:
    logger.warning("WEKNORA_CHAT_TIMEOUT is not a valid integer; falling back to 300s.")
    WEKNORA_CHAT_TIMEOUT = 300


class WeKnoraClient:
    """Client for interacting with WeKnora API"""

    def __init__(self, base_url: str, api_key: str):
        """Initialize the WeKnora API client with base URL and authentication"""
        self.base_url = base_url
        self.api_key = api_key
        # SSL verification: enabled by default. Set WEKNORA_VERIFY_SSL=false to disable
        # (e.g. for self-signed certs in dev environments — NOT recommended for production).
        self.verify_ssl = os.getenv("WEKNORA_VERIFY_SSL", "true").lower() != "false"
        if not self.verify_ssl:
            logger.warning(
                "SSL certificate verification is DISABLED (WEKNORA_VERIFY_SSL=false). "
                "This is insecure and should not be used in production."
            )
            urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
        # Create a persistent session for connection pooling and performance
        self.session = requests.Session()
        self.session.verify = self.verify_ssl
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

    # ── UUID pattern (8-4-4-4-12 hex) ──────────────────────────────────────
    _UUID_RE = re.compile(
        r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
        re.IGNORECASE,
    )

    def resolve_agent_id(self, agent_id_or_name: str) -> str:
        """Resolve an agent name to its UUID if needed.

        If *agent_id_or_name* is already a UUID it is returned unchanged.
        Otherwise all agents are listed and the first one whose
        ``name`` matches (case-insensitive) is returned.
        Raises ValueError when no match is found.
        """
        if self._UUID_RE.match(agent_id_or_name):
            return agent_id_or_name
        resp = self._request("GET", "/agents")
        agents = resp.get("data", resp) if isinstance(resp, dict) else resp
        if isinstance(agents, dict):
            agents = agents.get("list", agents.get("items", []))
        needle = agent_id_or_name.lower()
        for agent in (agents or []):
            if isinstance(agent, dict) and agent.get("name", "").lower() == needle:
                return agent["id"]
        raise ValueError(
            f"Agent {agent_id_or_name!r} not found. "
            "Use list_agents to see available agent IDs and names."
        )

    def resolve_kb_id(self, kb_id_or_name: str) -> str:
        """Resolve a knowledge base name to its UUID if needed.

        If *kb_id_or_name* is already a UUID it is returned unchanged.
        Otherwise all knowledge bases are listed and the first one whose
        ``name`` matches (case-insensitive) is returned.
        Raises ValueError when no match is found.
        """
        if self._UUID_RE.match(kb_id_or_name):
            return kb_id_or_name
        resp = self.list_knowledge_bases()
        kbs = resp.get("data", resp) if isinstance(resp, dict) else resp
        if isinstance(kbs, dict):
            kbs = kbs.get("list", kbs.get("items", []))
        needle = kb_id_or_name.lower()
        for kb in (kbs or []):
            if isinstance(kb, dict) and kb.get("name", "").lower() == needle:
                return kb["id"]
        raise ValueError(
            f"Knowledge base {kb_id_or_name!r} not found. "
            "Use list_knowledge_bases to see available IDs and names."
        )

    def hybrid_search(self, kb_id: str, query: str, config: Dict) -> Dict:
        """Perform hybrid search combining vector and keyword search"""
        data = {
            "query_text": query,
            **config,  # Include thresholds and match count
        }
        return self._request(
            "POST", f"/knowledge-bases/{kb_id}/hybrid-search", json=data
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
    def create_session(
        self,
        kb_id: str,
        max_rounds: int = 5,
        enable_rewrite: bool = True,
        fallback_response: str = "Sorry, I cannot answer this question.",
        summary_model_id: str = "",
        title: str = "",
        description: str = "",
    ) -> Dict:
        """Create a new chat session with strategy configuration"""
        strategy = {
            "max_rounds": max_rounds,
            "enable_rewrite": enable_rewrite,
            "fallback_strategy": "FIXED_RESPONSE",
            "fallback_response": fallback_response,
            "embedding_top_k": 10,
            "keyword_threshold": 0.5,
            "vector_threshold": 0.7,
            "summary_model_id": summary_model_id,
        }
        data = {
            "knowledge_base_id": kb_id,
            "session_strategy": strategy,
        }
        if title:
            data["title"] = title
        if description:
            data["description"] = description
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
    def _consume_sse_stream(self, url: str, body: Dict[str, Any]) -> Dict:
        """POST to *url* with *body*, consume the SSE stream, and return the assembled result.

        Centralised helper used by both chat() and agent_chat().
        Timeout: (10s connect, WEKNORA_CHAT_TIMEOUT read) — configurable via env var.
        
        Server-Sent Events (SSE) stream format:
          data: {"response_type": "answer", "content": "..."}
          data: {"response_type": "references", "knowledge_references": [...]}
          data: {"response_type": "complete"}
        
        We accumulate answer chunks and extract references, returning them as a dict.
        """
        try:
            # POST with stream=True to receive server-sent events incrementally
            # Timeout: 10s to establish connection, WEKNORA_CHAT_TIMEOUT for reading response
            response = self.session.post(
                url, json=body, stream=True,
                timeout=(10, WEKNORA_CHAT_TIMEOUT),
            )
            response.raise_for_status()

            answer_chunks: list = []
            references: list = []
            debug_events: list = []

            # Use context manager to ensure the connection is returned to the pool
            # even when breaking early on a 'complete' event.
            with response:
                for raw_line in response.iter_lines():
                    if not raw_line:
                        continue
                    if isinstance(raw_line, bytes):
                        raw_line = raw_line.decode("utf-8")
                    # Each SSE event is prefixed with "data: " followed by JSON payload
                    if not raw_line.startswith("data:"):
                        continue
                    payload = raw_line[5:].lstrip(" ")
                    try:
                        event_data = json.loads(payload)
                    except json.JSONDecodeError:
                        continue

                    response_type = event_data.get("response_type", "")
                    debug_events.append({"type": response_type, "content": event_data.get("content", "")[:80]})

                    # Parse different SSE event types: answer chunks, references, errors, completion
                    if response_type == "answer":
                        chunk = event_data.get("content", "")
                        if chunk:
                            answer_chunks.append(chunk)
                    elif response_type == "references":
                        references = event_data.get("knowledge_references") or []
                    elif response_type == "error":
                        raise RequestException(
                            f"Server error: {event_data.get('content', 'unknown error')}"
                        )
                    elif response_type == "complete":
                        break

            return {
                "answer": "".join(answer_chunks),
                "references": references,
                "_debug_events": debug_events,
            }
        except RequestException as e:
            logger.error(f"SSE stream request failed ({url}): {e}")
            raise

    def chat(
        self,
        session_id: str,
        query: str,
        knowledge_base_ids: list = None,
        web_search_enabled: bool = False,
        enable_memory: bool = False,
    ) -> Dict:
        """Send a message to the RAG pipeline (knowledge-chat) and return the assembled answer.

        Provide *knowledge_base_ids* (UUID or name) so the backend can retrieve
        relevant chunks before summarising with the LLM.
        For agentic tool-calling use agent_chat() instead.
        """
        url = f"{self.base_url}/knowledge-chat/{session_id}"
        body: Dict[str, Any] = {"query": query, "channel": "api"}
        if knowledge_base_ids:
            body["knowledge_base_ids"] = knowledge_base_ids
        if web_search_enabled:
            body["web_search_enabled"] = True
        if enable_memory:
            body["enable_memory"] = True
        result = self._consume_sse_stream(url, body)
        result["session_id"] = session_id
        return result

    def agent_chat(
        self,
        session_id: str,
        query: str,
        agent_id: str,
        knowledge_base_ids: list = None,
        web_search_enabled: bool = False,
        enable_memory: bool = False,
    ) -> Dict:
        """Send a message to the agentic pipeline (agent-chat) and return the assembled answer.

        *agent_id* is required — the backend uses the CustomAgent config for
        tool selection (knowledge_search, web_search, SQL, etc.).
        The agent autonomously decides which knowledge bases to query;
        pass *knowledge_base_ids* to override or supplement the agent's default KBs.
        """
        url = f"{self.base_url}/agent-chat/{session_id}"
        body: Dict[str, Any] = {"query": query, "agent_id": agent_id, "channel": "api"}
        if knowledge_base_ids:
            body["knowledge_base_ids"] = knowledge_base_ids
        if web_search_enabled:
            body["web_search_enabled"] = True
        if enable_memory:
            body["enable_memory"] = True
        result = self._consume_sse_stream(url, body)
        result["session_id"] = session_id
        return result

    def list_agents(self, page: int = 1, page_size: int = 50) -> Dict:
        """List all custom agents available to the current tenant."""
        return self._request("GET", "/agents", params={"page": page, "page_size": page_size})

    def get_agent(self, agent_id: str) -> Dict:
        """Get full config of a single agent by UUID."""
        return self._request("GET", f"/agents/{agent_id}")

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
            description="Create a new chat session with conversation strategy for a knowledge base",
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
            description=(
                "RAG pipeline chat: retrieve relevant chunks from knowledge bases, then summarise with LLM. "
                "ALWAYS provide knowledge_base_ids (names like 'my-knowledge-base' or UUIDs) so retrieval can run — "
                "without them the answer is based on LLM knowledge only. "
                "Use list_knowledge_bases to discover available knowledge bases. "
                "For multi-step reasoning or tool-calling use agent_chat instead."
            ),
            inputSchema={
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
                    "enable_memory": {"type": "boolean", "description": "Enable cross-session memory.", "default": False},
                },
                "required": ["session_id", "query"],
            },
        ),
        types.Tool(
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
            inputSchema={
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
                    "enable_memory": {"type": "boolean", "description": "Enable cross-session memory.", "default": False},
                },
                "required": ["session_id", "query", "agent_id"],
            },
        ),
        types.Tool(
            name="list_agents",
            description="List all custom agents available to the current tenant. Use this to discover agent IDs, names, and their KB selection mode before calling agent_chat.",
            inputSchema={
                "type": "object",
                "properties": {
                    "page": {"type": "integer", "description": "Page number", "default": 1},
                    "page_size": {"type": "integer", "description": "Page size", "default": 50},
                },
                "required": [],
            },
        ),
        types.Tool(
            name="get_agent",
            description=(
                "Get full configuration of a single agent by UUID or name. "
                "Check kb_selection_mode and knowledge_bases fields: "
                "if kb_selection_mode is 'none' or 'selected' with an empty knowledge_bases list, "
                "you MUST pass knowledge_base_ids when calling agent_chat."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "agent_id": {"type": "string", "description": "Agent UUID or name"},
                },
                "required": ["agent_id"],
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
            kb_id = client.resolve_kb_id(args["kb_id"])
            result = client.hybrid_search(kb_id, args["query"], config)

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
            # Create a knowledge-base-bound chat session with strategy configuration.
            # Strategy includes: max conversation rounds, query rewriting, summarization model,
            # fallback response handling, and retrieval thresholds (keyword/vector similarity).
            result = client.create_session(
                kb_id=client.resolve_kb_id(args["kb_id"]),
                max_rounds=args.get("max_rounds", 5),
                enable_rewrite=args.get("enable_rewrite", True),
                fallback_response=args.get(
                    "fallback_response", "Sorry, I cannot answer this question."
                ),
                summary_model_id=args.get("summary_model_id", ""),
                title=args.get("title", ""),
                description=args.get("description", ""),
            )
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
            # Resolve KB names → UUIDs to support both human-friendly names and UUIDs
            raw_kb_ids = args.get("knowledge_base_ids") or []
            kb_ids = [client.resolve_kb_id(k) for k in raw_kb_ids] if raw_kb_ids else None
            # Use run_in_executor to avoid blocking the async event loop during
            # network I/O and SSE streaming. This allows concurrent request handling.
            fn = functools.partial(
                client.chat,
                args["session_id"],
                args["query"],
                knowledge_base_ids=kb_ids,
                web_search_enabled=args.get("web_search_enabled", False),
                enable_memory=args.get("enable_memory", False),
            )
            # get_running_loop() is the correct API inside async functions (get_event_loop() is deprecated)
            result = await asyncio.get_running_loop().run_in_executor(None, fn)

        elif name == "agent_chat":
            # Autonomous agent tool-calling: agent decides which tools to invoke (knowledge_search, web_search, etc.)
            # Unlike RAG chat, the agent pipeline allows multi-step reasoning with explicit tool calls.
            # Resolve required agent name → UUID
            agent_id = client.resolve_agent_id(args["agent_id"])
            # Resolve optional KB overrides (agent may have built-in KBs but user can override)
            raw_kb_ids = args.get("knowledge_base_ids") or []
            kb_ids = [client.resolve_kb_id(k) for k in raw_kb_ids] if raw_kb_ids else None
            # Pre-check: if no KB IDs provided, inspect agent config to detect
            # kb_selection_mode=none/selected-empty so we fail fast with a clear message
            # instead of the cryptic backend error "no search targets available".
            if not kb_ids:
                try:
                    # Fetch agent configuration to check KB requirements
                    agent_info = client.get_agent(agent_id)
                    cfg = (agent_info.get("data") or agent_info).get("config") or {}
                    mode = cfg.get("kb_selection_mode", "selected")
                    built_in_kbs = cfg.get("knowledge_bases") or []
                    # If mode=none or (mode=selected and no built-in KBs), agent requires explicit KB selection
                    needs_kbs = (mode == "none") or (mode in ("selected", "") and not built_in_kbs)
                    if needs_kbs:
                        kb_list = client.list_knowledge_bases()
                        kbs = (kb_list.get("data") or kb_list)
                        if isinstance(kbs, dict):
                            kbs = kbs.get("list", kbs.get("items", []))
                        kb_summary = ", ".join(
                            f"{kb.get('name')} ({kb.get('id')})"
                            for kb in (kbs or [])[:10]
                            if isinstance(kb, dict)
                        )
                        raise ValueError(
                            f"Agent '{args['agent_id']}' has kb_selection_mode='{mode}' with no built-in "
                            f"knowledge bases. You must provide knowledge_base_ids. "
                            f"Available knowledge bases: [{kb_summary}]"
                        )
                except ValueError:
                    raise
                except Exception as preflight_err:
                    logger.warning(f"agent_chat preflight KB check failed (non-fatal): {preflight_err}")
            fn = functools.partial(
                client.agent_chat,
                args["session_id"],
                args["query"],
                agent_id,
                knowledge_base_ids=kb_ids,
                web_search_enabled=args.get("web_search_enabled", False),
                enable_memory=args.get("enable_memory", False),
            )
            result = await asyncio.get_running_loop().run_in_executor(None, fn)

        elif name == "list_agents":
            result = client.list_agents(
                page=args.get("page", 1),
                page_size=args.get("page_size", 50),
            )

        elif name == "get_agent":
            resolved_id = client.resolve_agent_id(args["agent_id"])
            result = client.get_agent(resolved_id)

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


def _init_options() -> InitializationOptions:
    """Build MCP InitializationOptions (shared across all transports)"""
    return InitializationOptions(
        server_name="weknora-server",
        server_version="1.0.0",
        capabilities=app.get_capabilities(
            notification_options=NotificationOptions(),
            experimental_capabilities={},
        ),
    )


async def run_stdio():
    """Run the MCP server using stdio transport"""
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        await app.run(read_stream, write_stream, _init_options())


async def run_sse(host: str, port: int):
    """Run the MCP server using SSE transport (legacy MCP clients)"""
    try:
        from mcp.server.sse import SseServerTransport
        from starlette.applications import Starlette
        from starlette.routing import Mount
        import uvicorn
    except ImportError as e:
        raise ImportError(
            f"SSE transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn\n{e}"
        ) from e

    sse = SseServerTransport("/messages/")

    # Use a raw ASGI callable instead of a Starlette Request endpoint to avoid
    # accessing Starlette's private _send attribute (which can break across versions).
    async def handle_sse(scope, receive, send):
        async with sse.connect_sse(scope, receive, send) as streams:
            await app.run(streams[0], streams[1], _init_options())

    starlette_app = Starlette(
        routes=[
            Mount("/sse", app=handle_sse),
            Mount("/messages/", app=sse.handle_post_message),
        ]
    )

    logger.info("Starting SSE MCP server on %s:%d", host, port)
    logger.info("SSE endpoint:  http://%s:%d/sse", host, port)
    config = uvicorn.Config(starlette_app, host=host, port=port, log_level="info")
    server = uvicorn.Server(config)
    await server.serve()


async def run_http(host: str, port: int):
    """Run the MCP server using Streamable HTTP transport (MCP 2025-03-26 spec)"""
    try:
        from contextlib import asynccontextmanager
        from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
        from starlette.applications import Starlette
        from starlette.routing import Mount
        import uvicorn
    except ImportError as e:
        raise ImportError(
            f"HTTP transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn\n{e}"
        ) from e

    session_manager = StreamableHTTPSessionManager(
        app=app,
        event_store=None,
        json_response=False,
        stateless=True,
    )

    @asynccontextmanager
    async def lifespan(_app):
        async with session_manager.run():
            yield

    starlette_app = Starlette(
        routes=[Mount("/", app=session_manager.handle_request)],
        lifespan=lifespan,
    )

    logger.info("Starting Streamable HTTP MCP server on %s:%d", host, port)
    logger.info("MCP endpoint:  http://%s:%d/mcp", host, port)
    config = uvicorn.Config(starlette_app, host=host, port=port, log_level="info")
    server = uvicorn.Server(config)
    await server.serve()


# Backward-compatible alias used by run_server.py
run = run_stdio


def main():
    """Main entry point — supports stdio, sse, and http transports.

    Transport selection (in priority order):
      1. --transport CLI flag
      2. MCP_TRANSPORT environment variable
      3. Default: stdio
    """
    parser = argparse.ArgumentParser(description="WeKnora MCP Server")
    parser.add_argument(
        "--transport",
        choices=["stdio", "sse", "http"],
        default=os.getenv("MCP_TRANSPORT", "stdio"),
        help="Transport type: stdio (default), sse, or http",
    )
    parser.add_argument(
        "--host",
        default=os.getenv("MCP_HOST", "0.0.0.0"),
        help="Bind host for network transports (default: 0.0.0.0)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.getenv("MCP_PORT", "8000")),
        help="Bind port for network transports (default: 8000)",
    )
    args = parser.parse_args()

    if args.transport == "stdio":
        asyncio.run(run_stdio())
    elif args.transport == "sse":
        asyncio.run(run_sse(args.host, args.port))
    elif args.transport == "http":
        asyncio.run(run_http(args.host, args.port))


if __name__ == "__main__":
    main()
