"""Authentication helpers for network MCP transports."""

from __future__ import annotations

import logging
import secrets
import sys

from weknora_mcp.config import network_transport_auth_token

logger = logging.getLogger(__name__)


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
            key.decode("latin-1").lower(): value.decode("latin-1")
            for key, value in scope.get("headers", [])
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
