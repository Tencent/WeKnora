"""Transport entrypoints for the WeKnora MCP server."""

from __future__ import annotations

import argparse
import asyncio
import logging
import os

import mcp.server.stdio
from mcp.server import Server

from upload_paths import set_active_transport
from weknora_mcp.auth import MCPAuthMiddleware, require_network_transport_auth
from weknora_mcp.server import create_server, initialization_options

logger = logging.getLogger(__name__)


def _server() -> Server:
    return create_server()


async def run_stdio() -> None:
    """Run the MCP server using stdio transport."""
    set_active_transport("stdio")
    server = _server()
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        await server.run(read_stream, write_stream, initialization_options(server))


async def run_sse(host: str, port: int) -> None:
    """Run the MCP server using SSE transport (legacy MCP clients)."""
    set_active_transport("sse")
    auth_token = require_network_transport_auth("sse")
    try:
        from mcp.server.sse import SseServerTransport
        from starlette.applications import Starlette
        from starlette.routing import Mount
        import uvicorn
    except ImportError as exc:
        raise ImportError(
            "SSE transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn"
        ) from exc

    server = _server()
    init_options = initialization_options(server)
    sse = SseServerTransport("/messages/")

    async def handle_sse(scope, receive, send):
        async with sse.connect_sse(scope, receive, send) as streams:
            await server.run(streams[0], streams[1], init_options)

    starlette_app = Starlette(
        routes=[
            Mount("/sse/messages/", app=sse.handle_post_message),
            Mount("/sse", app=handle_sse),
        ]
    )
    starlette_app = MCPAuthMiddleware(starlette_app, auth_token)

    logger.info("Starting SSE MCP server on %s:%d", host, port)
    logger.info("SSE endpoint:  http://%s:%d/sse", host, port)
    config = uvicorn.Config(starlette_app, host=host, port=port, log_level="info")
    await uvicorn.Server(config).serve()


async def run_http(host: str, port: int) -> None:
    """Run the MCP server using Streamable HTTP transport."""
    set_active_transport("http")
    auth_token = require_network_transport_auth("http")
    try:
        from contextlib import asynccontextmanager

        from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
        from starlette.applications import Starlette
        from starlette.routing import Mount
        import uvicorn
    except ImportError as exc:
        raise ImportError(
            "HTTP transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn"
        ) from exc

    server = _server()
    session_manager = StreamableHTTPSessionManager(
        app=server,
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
    starlette_app = MCPAuthMiddleware(starlette_app, auth_token)

    logger.info("Starting Streamable HTTP MCP server on %s:%d", host, port)
    logger.info("MCP endpoint:  http://%s:%d/mcp", host, port)
    config = uvicorn.Config(starlette_app, host=host, port=port, log_level="info")
    await uvicorn.Server(config).serve()


# Backward-compatible alias used by run_server.py
run = run_stdio


def main() -> None:
    """CLI entry point supporting stdio, sse, and http transports."""
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
