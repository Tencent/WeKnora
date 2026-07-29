#!/usr/bin/env python3
"""
WeKnora MCP Server

Backward-compatible module entrypoint. New code should import from `weknora_mcp`.
"""

from __future__ import annotations

import logging

from weknora_mcp import (
    WeKnoraClient,
    create_client,
    create_server,
    main,
    run,
    run_http,
    run_sse,
    run_stdio,
)
from weknora_mcp.config import (
    SERVER_NAME,
    SERVER_VERSION,
    WEKNORA_API_KEY,
    WEKNORA_BASE_URL,
    WEKNORA_CHAT_TIMEOUT,
    network_transport_auth_token,
)
from weknora_mcp.auth import MCPAuthMiddleware, require_network_transport_auth

logging.basicConfig(level=logging.INFO)

client = create_client()
app = create_server(client)

__all__ = [
    "WeKnoraClient",
    "MCPAuthMiddleware",
    "SERVER_NAME",
    "SERVER_VERSION",
    "WEKNORA_API_KEY",
    "WEKNORA_BASE_URL",
    "WEKNORA_CHAT_TIMEOUT",
    "app",
    "client",
    "create_client",
    "create_server",
    "main",
    "network_transport_auth_token",
    "require_network_transport_auth",
    "run",
    "run_http",
    "run_sse",
    "run_stdio",
]

if __name__ == "__main__":
    main()
