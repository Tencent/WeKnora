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
import secrets
import sys
import threading
from typing import Any, Dict

import urllib3
import requests
from mcp.server import MCPServer
from requests.exceptions import RequestException
from upload_paths import resolve_upload_file_path, set_active_transport

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

# Network transport defaults kept for backward compatibility with pre-2.x deployments.
SSE_MESSAGE_PATH = "/sse/messages/"
STREAMABLE_HTTP_STATELESS = True


def network_transport_auth_token() -> str:
    """Shared secret clients must present for SSE/HTTP transports."""
    return os.getenv("MCP_SERVER_AUTH_TOKEN", "").strip()


def require_network_transport_auth(transport: str) -> str:
    """SSE/HTTP must not start without a configured auth token."""
    token = network_transport_auth_token()
    if transport in ("sse", "http") and not token:
        logger.error(
            "MCP_SERVER_AUTH_TOKEN is required for %s transport. "
            "Set a strong shared secret; clients must send "
            "Authorization: Bearer <token> or X-MCP-Auth-Token.",
            transport,
        )
        sys.exit(1)
    return token


class MCPAuthMiddleware:
    """ASGI middleware that gates network MCP transports behind a shared secret."""

    def __init__(self, app, token: str):
        self.app = app
        self.token = token

    async def __call__(self, scope, receive, send):
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return

        headers = {
            k.decode("latin-1").lower(): v.decode("latin-1")
            for k, v in scope.get("headers", [])
        }
        provided = ""
        auth = headers.get("authorization", "")
        if auth.lower().startswith("bearer "):
            provided = auth[7:].strip()
        elif "x-mcp-auth-token" in headers:
            provided = headers["x-mcp-auth-token"]

        if not provided or not secrets.compare_digest(provided, self.token):
            body = b'{"error":"unauthorized"}'
            await send(
                {
                    "type": "http.response.start",
                    "status": 401,
                    "headers": [[b"content-type", b"application/json"]],
                }
            )
            await send({"type": "http.response.body", "body": body})
            return

        await self.app(scope, receive, send)


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
        # MCP 2.x runs sync @mcp.tool() handlers on worker threads; use a
        # thread-local Session because requests.Session is not thread-safe.
        self._session_local = threading.local()

    def _new_session(self) -> requests.Session:
        session = requests.Session()
        session.verify = self.verify_ssl
        session.headers.update(
            {
                "X-API-Key": self.api_key,
                "Content-Type": "application/json",
            }
        )
        return session

    @property
    def session(self) -> requests.Session:
        if not getattr(self._session_local, "session", None):
            self._session_local.session = self._new_session()
        return self._session_local.session

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
        safe_path = resolve_upload_file_path(file_path)
        with open(safe_path, "rb") as f:
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
                verify=self.verify_ssl,
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


# Initialize MCP server instance (mcp 2.x high-level API).
# MCPServer (formerly FastMCP) builds input schemas from function type hints
# and serializes plain return values automatically.
mcp = MCPServer("weknora-server", version="1.1.1")
# Initialize WeKnora API client with configuration
client = WeKnoraClient(WEKNORA_BASE_URL, WEKNORA_API_KEY)


# ---------------------------------------------------------------------------
# Tool registrations
#
# Each tool is a plain function decorated with @mcp.tool(). Parameters are
# declared via type hints (the framework derives the JSON Schema); required
# parameters have no default. Descriptions come from the docstring. Tools
# return dicts/str and the framework handles serialization and error wrapping.
# Blocking network I/O (chat / agent_chat) is offloaded to a thread executor
# so the async event loop is not blocked.
# ---------------------------------------------------------------------------


@mcp.tool()
def create_tenant(
    name: str,
    description: str,
    business: str,
    retriever_engines: dict | None = None,
) -> dict:
    """Create a new tenant in WeKnora."""
    engines = retriever_engines or {
        "engines": [
            {"retriever_type": "keywords", "retriever_engine_type": "postgres"},
            {"retriever_type": "vector", "retriever_engine_type": "postgres"},
        ]
    }
    return client.create_tenant(name, description, business, engines)


@mcp.tool()
def list_tenants() -> dict:
    """List all tenants."""
    return client.list_tenants()


@mcp.tool()
def create_knowledge_base(
    name: str,
    description: str,
    embedding_model_id: str = "",
    summary_model_id: str = "",
) -> dict:
    """Create a new knowledge base."""
    config = {
        "chunking_config": {
            "chunk_size": 1000,
            "chunk_overlap": 200,
            "separators": ["."],
            "enable_multimodal": True,
        },
        "embedding_model_id": embedding_model_id,
        "summary_model_id": summary_model_id,
    }
    return client.create_knowledge_base(name, description, config)


@mcp.tool()
def list_knowledge_bases() -> dict:
    """List all knowledge bases."""
    return client.list_knowledge_bases()


@mcp.tool()
def get_knowledge_base(kb_id: str) -> dict:
    """Get knowledge base details."""
    return client.get_knowledge_base(kb_id)


@mcp.tool()
def delete_knowledge_base(kb_id: str) -> dict:
    """Delete a knowledge base."""
    return client.delete_knowledge_base(kb_id)


@mcp.tool()
def hybrid_search(
    kb_id: str,
    query: str,
    vector_threshold: float = 0.5,
    keyword_threshold: float = 0.3,
    match_count: int = 5,
) -> dict:
    """Perform hybrid (vector + keyword) search in a knowledge base.

    kb_id may be a UUID or a knowledge-base name (resolved automatically).
    Use list_knowledge_bases to discover available knowledge bases.
    """
    config = {
        "vector_threshold": vector_threshold,
        "keyword_threshold": keyword_threshold,
        "match_count": match_count,
    }
    resolved = client.resolve_kb_id(kb_id)
    return client.hybrid_search(resolved, query, config)


@mcp.tool()
def create_knowledge_from_file(
    kb_id: str,
    file_path: str,
    enable_multimodel: bool = True,
) -> dict:
    """Create knowledge from a local file on the server filesystem."""
    return client.create_knowledge_from_file(kb_id, file_path, enable_multimodel)


@mcp.tool()
def create_knowledge_from_url(
    kb_id: str,
    url: str,
    enable_multimodel: bool = True,
) -> dict:
    """Create knowledge from a web URL."""
    return client.create_knowledge_from_url(kb_id, url, enable_multimodel)


@mcp.tool()
def list_knowledge(kb_id: str, page: int = 1, page_size: int = 20) -> dict:
    """List knowledge entries in a knowledge base."""
    return client.list_knowledge(kb_id, page, page_size)


@mcp.tool()
def get_knowledge(knowledge_id: str) -> dict:
    """Get knowledge details."""
    return client.get_knowledge(knowledge_id)


@mcp.tool()
def delete_knowledge(knowledge_id: str) -> dict:
    """Delete a knowledge entry."""
    return client.delete_knowledge(knowledge_id)


@mcp.tool()
def create_model(
    name: str,
    type: str,
    description: str,
    source: str = "local",
    base_url: str = "",
    api_key: str = "",
    is_default: bool = False,
) -> dict:
    """Create a new model configuration (type: KnowledgeQA, Embedding, or Rerank)."""
    parameters = {"base_url": base_url, "api_key": api_key}
    return client.create_model(name, type, source, description, parameters, is_default)


@mcp.tool()
def list_models() -> dict:
    """List all models."""
    return client.list_models()


@mcp.tool()
def get_model(model_id: str) -> dict:
    """Get model details."""
    return client.get_model(model_id)


@mcp.tool()
def create_session(
    kb_id: str,
    max_rounds: int = 5,
    enable_rewrite: bool = True,
    fallback_response: str = "Sorry, I cannot answer this question.",
    summary_model_id: str = "",
    title: str = "",
    description: str = "",
) -> dict:
    """Create a new chat session bound to a knowledge base with a retrieval strategy.

    kb_id may be a UUID or a knowledge-base name (resolved automatically).
    """
    return client.create_session(
        kb_id=client.resolve_kb_id(kb_id),
        max_rounds=max_rounds,
        enable_rewrite=enable_rewrite,
        fallback_response=fallback_response,
        summary_model_id=summary_model_id,
        title=title,
        description=description,
    )


@mcp.tool()
def get_session(session_id: str) -> dict:
    """Get session details."""
    return client.get_session(session_id)


@mcp.tool()
def list_sessions(page: int = 1, page_size: int = 20) -> dict:
    """List chat sessions."""
    return client.list_sessions(page, page_size)


@mcp.tool()
def delete_session(session_id: str) -> dict:
    """Delete a session."""
    return client.delete_session(session_id)


@mcp.tool()
async def chat(
    session_id: str,
    query: str,
    knowledge_base_ids: list[str] | None = None,
    web_search_enabled: bool = False,
) -> dict:
    """RAG pipeline chat: retrieve relevant chunks from knowledge bases, then summarise with LLM.

    ALWAYS provide knowledge_base_ids (names like 'my-knowledge-base' or UUIDs) so
    retrieval can run — without them the answer is based on LLM knowledge only.
    Use list_knowledge_bases to discover available knowledge bases.
    For multi-step reasoning or tool-calling use agent_chat instead.
    """
    kb_ids = (
        [client.resolve_kb_id(k) for k in knowledge_base_ids]
        if knowledge_base_ids
        else None
    )
    fn = functools.partial(
        client.chat,
        session_id,
        query,
        knowledge_base_ids=kb_ids,
        web_search_enabled=web_search_enabled,
    )
    # get_running_loop() is the correct API inside async functions.
    return await asyncio.get_running_loop().run_in_executor(None, fn)


@mcp.tool()
async def agent_chat(
    session_id: str,
    query: str,
    agent_id: str,
    knowledge_base_ids: list[str] | None = None,
    web_search_enabled: bool = False,
) -> dict:
    """Agentic pipeline chat: the agent autonomously calls tools (knowledge_search, web_search, SQL, etc.).

    REQUIRED: agent_id (name or UUID) — use list_agents to discover agents.
    IMPORTANT: many agents have KBSelectionMode=none and NO built-in knowledge bases.
    In that case you MUST pass knowledge_base_ids, otherwise the agent will fail
    with 'no search targets available'. Use get_agent to inspect an agent's
    kb_selection_mode and knowledge_bases before calling. If kb_selection_mode is
    'none' or 'selected' with an empty list, always provide knowledge_base_ids.
    """
    resolved_agent_id = client.resolve_agent_id(agent_id)
    kb_ids = (
        [client.resolve_kb_id(k) for k in knowledge_base_ids]
        if knowledge_base_ids
        else None
    )
    # Pre-check: if no KB IDs provided, inspect agent config to detect
    # kb_selection_mode=none/selected-empty so we fail fast with a clear message
    # instead of the cryptic backend error "no search targets available".
    if not kb_ids:
        try:
            agent_info = client.get_agent(resolved_agent_id)
            cfg = (agent_info.get("data") or agent_info).get("config") or {}
            mode = cfg.get("kb_selection_mode", "selected")
            built_in_kbs = cfg.get("knowledge_bases") or []
            needs_kbs = (mode == "none") or (
                mode in ("selected", "") and not built_in_kbs
            )
            if needs_kbs:
                kb_list = client.list_knowledge_bases()
                kbs = kb_list.get("data") or kb_list
                if isinstance(kbs, dict):
                    kbs = kbs.get("list", kbs.get("items", []))
                kb_summary = ", ".join(
                    f"{kb.get('name')} ({kb.get('id')})"
                    for kb in (kbs or [])[:10]
                    if isinstance(kb, dict)
                )
                raise ValueError(
                    f"Agent '{agent_id}' has kb_selection_mode='{mode}' with no built-in "
                    f"knowledge bases. You must provide knowledge_base_ids. "
                    f"Available knowledge bases: [{kb_summary}]"
                )
        except ValueError:
            raise
        except Exception as preflight_err:
            logger.warning(
                "agent_chat preflight KB check failed (non-fatal): %s", preflight_err
            )
    fn = functools.partial(
        client.agent_chat,
        session_id,
        query,
        resolved_agent_id,
        knowledge_base_ids=kb_ids,
        web_search_enabled=web_search_enabled,
    )
    return await asyncio.get_running_loop().run_in_executor(None, fn)


@mcp.tool()
def list_agents(page: int = 1, page_size: int = 50) -> dict:
    """List all custom agents available to the current tenant.

    Use this to discover agent IDs, names, and their KB selection mode before
    calling agent_chat.
    """
    return client.list_agents(page=page, page_size=page_size)


@mcp.tool()
def get_agent(agent_id: str) -> dict:
    """Get full configuration of a single agent by UUID or name.

    Check kb_selection_mode and knowledge_bases fields: if kb_selection_mode is
    'none' or 'selected' with an empty knowledge_bases list, you MUST pass
    knowledge_base_ids when calling agent_chat.
    """
    resolved_id = client.resolve_agent_id(agent_id)
    return client.get_agent(resolved_id)


@mcp.tool()
def list_chunks(knowledge_id: str, page: int = 1, page_size: int = 20) -> dict:
    """List chunks (text segments) of a knowledge entry."""
    return client.list_chunks(knowledge_id, page, page_size)


@mcp.tool()
def delete_chunk(knowledge_id: str, chunk_id: str) -> dict:
    """Delete a chunk."""
    return client.delete_chunk(knowledge_id, chunk_id)


@mcp.tool()
def wiki_search(kb_id: str, query: str, limit: int = 10) -> dict:
    """Search wiki pages by full-text query.

    Returns matching wiki pages with title, slug, summary, and content snippets.
    """
    return client.wiki_search(kb_id, query, limit)


@mcp.tool()
def wiki_read_page(kb_id: str, slug: str) -> dict:
    """Read a wiki page by its slug.

    Returns full markdown content, metadata, inbound/outbound links, and source
    references. slug example: 'entity/acme-corp', 'concept/rag'.
    """
    return client.wiki_read_page(kb_id, slug)


@mcp.tool()
def wiki_index_view(kb_id: str, limit: int = 50) -> dict:
    """Get a structured wiki index with per-type directory groups.

    Returns an overview of all wiki pages organized by type (entity, concept,
    summary, etc.).
    """
    return client.wiki_index_view(kb_id, limit)


# ---------------------------------------------------------------------------
# Transports
# ---------------------------------------------------------------------------


async def run_stdio():
    """Run the MCP server using stdio transport."""
    set_active_transport("stdio")
    await mcp.run_stdio_async()


async def run_sse(host: str, port: int):
    """Run the MCP server using SSE transport (legacy MCP clients)."""
    set_active_transport("sse")
    auth_token = require_network_transport_auth("sse")
    try:
        import uvicorn
    except ImportError as e:
        raise ImportError(
            f"SSE transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn\n{e}"
        ) from e

    starlette_app = MCPAuthMiddleware(
        mcp.sse_app(host=host, message_path=SSE_MESSAGE_PATH),
        auth_token,
    )

    logger.info("Starting SSE MCP server on %s:%d", host, port)
    logger.info("SSE endpoint:  http://%s:%d/sse", host, port)
    logger.info("SSE messages: http://%s:%d%s", host, port, SSE_MESSAGE_PATH)
    config = uvicorn.Config(starlette_app, host=host, port=port, log_level="info")
    server = uvicorn.Server(config)
    await server.serve()


async def run_http(host: str, port: int):
    """Run the MCP server using Streamable HTTP transport (MCP 2025-03-26 spec)."""
    set_active_transport("http")
    auth_token = require_network_transport_auth("http")
    try:
        import uvicorn
    except ImportError as e:
        raise ImportError(
            f"HTTP transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn\n{e}"
        ) from e

    starlette_app = MCPAuthMiddleware(
        mcp.streamable_http_app(host=host, stateless_http=STREAMABLE_HTTP_STATELESS),
        auth_token,
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
        default=os.getenv("MCP_HOST", "127.0.0.1"),
        help="Bind host for network transports (default: 127.0.0.1)",
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
