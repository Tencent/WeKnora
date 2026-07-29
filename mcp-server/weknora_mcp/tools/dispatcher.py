"""Route MCP tool calls to the WeKnora REST API."""

from __future__ import annotations

import asyncio
import functools
import logging
from typing import Any

from mcp.types import CallToolResult

from weknora_mcp.client import WeKnoraClient
from weknora_mcp.tools.results import tool_failure, tool_success

logger = logging.getLogger(__name__)


class ToolDispatcher:
    """Execute MCP tools against a WeKnora API client."""

    def __init__(self, client: WeKnoraClient):
        self._client = client

    async def dispatch(self, name: str, arguments: dict | None) -> CallToolResult:
        args = arguments or {}
        try:
            result = await self._execute(name, args)
            return tool_success(result)
        except Exception as exc:
            logger.error("Tool execution failed: %s", exc)
            return tool_failure(f"Error executing {name}: {exc}")

    async def _execute(self, name: str, args: dict[str, Any]) -> Any:
        # Tenant Management - Route tenant-related operations
        if name == "create_tenant":
            result = self._client.create_tenant(
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
            result = self._client.list_tenants()

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
            result = self._client.create_knowledge_base(
                args["name"], args["description"], config
            )
        elif name == "list_knowledge_bases":
            result = self._client.list_knowledge_bases()
        elif name == "get_knowledge_base":
            result = self._client.get_knowledge_base(args["kb_id"])
        elif name == "delete_knowledge_base":
            result = self._client.delete_knowledge_base(args["kb_id"])
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
            kb_id = self._client.resolve_kb_id(args["kb_id"])
            result = self._client.hybrid_search(kb_id, args["query"], config)

        # Knowledge Management
        elif name == "create_knowledge_from_file":
            result = self._client.create_knowledge_from_file(
                args["kb_id"], args["file_path"], args.get("enable_multimodel", True)
            )
        elif name == "create_knowledge_from_url":
            result = self._client.create_knowledge_from_url(
                args["kb_id"], args["url"], args.get("enable_multimodel", True)
            )
        elif name == "list_knowledge":
            result = self._client.list_knowledge(
                args["kb_id"], args.get("page", 1), args.get("page_size", 20)
            )
        elif name == "get_knowledge":
            result = self._client.get_knowledge(args["knowledge_id"])
        elif name == "delete_knowledge":
            result = self._client.delete_knowledge(args["knowledge_id"])

        # Model Management - Route model configuration operations
        elif name == "create_model":
            # Build model parameters (API credentials, endpoints, etc.)
            parameters = {
                "base_url": args.get("base_url", ""),  # Model API endpoint
                "api_key": args.get("api_key", ""),  # Model API key
            }
            result = self._client.create_model(
                args["name"],
                args["type"],
                args.get("source", "local"),
                args["description"],
                parameters,
                args.get("is_default", False),
            )
        elif name == "list_models":
            result = self._client.list_models()
        elif name == "get_model":
            result = self._client.get_model(args["model_id"])

        # Session Management - Route chat session operations
        elif name == "create_session":
            # Create a knowledge-base-bound chat session with strategy configuration.
            # Strategy includes: max conversation rounds, query rewriting, summarization model,
            # fallback response handling, and retrieval thresholds (keyword/vector similarity).
            result = self._client.create_session(
                kb_id=self._client.resolve_kb_id(args["kb_id"]),
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
            result = self._client.get_session(args["session_id"])
        elif name == "list_sessions":
            result = self._client.list_sessions(
                args.get("page", 1), args.get("page_size", 20)
            )
        elif name == "delete_session":
            result = self._client.delete_session(args["session_id"])

        # Chat Functionality
        elif name == "chat":
            # Resolve KB names → UUIDs to support both human-friendly names and UUIDs
            raw_kb_ids = args.get("knowledge_base_ids") or []
            kb_ids = [self._client.resolve_kb_id(k) for k in raw_kb_ids] if raw_kb_ids else None
            # Use run_in_executor to avoid blocking the async event loop during
            # network I/O and SSE streaming. This allows concurrent request handling.
            fn = functools.partial(
                self._client.chat,
                args["session_id"],
                args["query"],
                knowledge_base_ids=kb_ids,
                web_search_enabled=args.get("web_search_enabled", False),
            )
            # get_running_loop() is the correct API inside async functions (get_event_loop() is deprecated)
            result = await asyncio.get_running_loop().run_in_executor(None, fn)

        elif name == "agent_chat":
            # Autonomous agent tool-calling: agent decides which tools to invoke (knowledge_search, web_search, etc.)
            # Unlike RAG chat, the agent pipeline allows multi-step reasoning with explicit tool calls.
            # Resolve required agent name → UUID
            agent_id = self._client.resolve_agent_id(args["agent_id"])
            # Resolve optional KB overrides (agent may have built-in KBs but user can override)
            raw_kb_ids = args.get("knowledge_base_ids") or []
            kb_ids = [self._client.resolve_kb_id(k) for k in raw_kb_ids] if raw_kb_ids else None
            # Pre-check: if no KB IDs provided, inspect agent config to detect
            # kb_selection_mode=none/selected-empty so we fail fast with a clear message
            # instead of the cryptic backend error "no search targets available".
            if not kb_ids:
                try:
                    # Fetch agent configuration to check KB requirements
                    agent_info = self._client.get_agent(agent_id)
                    cfg = (agent_info.get("data") or agent_info).get("config") or {}
                    mode = cfg.get("kb_selection_mode", "selected")
                    built_in_kbs = cfg.get("knowledge_bases") or []
                    # If mode=none or (mode=selected and no built-in KBs), agent requires explicit KB selection
                    needs_kbs = (mode == "none") or (mode in ("selected", "") and not built_in_kbs)
                    if needs_kbs:
                        kb_list = self._client.list_knowledge_bases()
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
                self._client.agent_chat,
                args["session_id"],
                args["query"],
                agent_id,
                knowledge_base_ids=kb_ids,
                web_search_enabled=args.get("web_search_enabled", False),
            )
            result = await asyncio.get_running_loop().run_in_executor(None, fn)

        elif name == "list_agents":
            result = self._client.list_agents(
                page=args.get("page", 1),
                page_size=args.get("page_size", 50),
            )

        elif name == "get_agent":
            resolved_id = self._client.resolve_agent_id(args["agent_id"])
            result = self._client.get_agent(resolved_id)

        # Chunk Management
        elif name == "list_chunks":
            result = self._client.list_chunks(
                args["knowledge_id"], args.get("page", 1), args.get("page_size", 20)
            )
        elif name == "delete_chunk":
            result = self._client.delete_chunk(args["knowledge_id"], args["chunk_id"])

        # Wiki Read-Only - Route wiki query operations
        elif name == "wiki_search":
            result = self._client.wiki_search(
                args["kb_id"], args["query"], args.get("limit", 10)
            )
        elif name == "wiki_read_page":
            result = self._client.wiki_read_page(args["kb_id"], args["slug"])
        elif name == "wiki_index_view":
            result = self._client.wiki_index_view(
                args["kb_id"], args.get("limit", 50)
            )
        else:
            raise ValueError(f"Unknown tool: {name}")

        return result
