"""MCP server assembly for WeKnora."""

from __future__ import annotations

from mcp.server import NotificationOptions, Server
from mcp.server.context import ServerRequestContext
from mcp.server.models import InitializationOptions
from mcp.types import CallToolRequestParams, ListToolsResult, PaginatedRequestParams

from weknora_mcp.client import WeKnoraClient
from weknora_mcp.config import SERVER_NAME, SERVER_VERSION, WEKNORA_API_KEY, WEKNORA_BASE_URL
from weknora_mcp.tools import ToolDispatcher, build_tools


def create_client() -> WeKnoraClient:
    return WeKnoraClient(WEKNORA_BASE_URL, WEKNORA_API_KEY)


def create_server(client: WeKnoraClient | None = None) -> Server:
    """Build the low-level MCP server with v2 constructor handlers."""
    api_client = client or create_client()
    dispatcher = ToolDispatcher(api_client)

    async def on_list_tools(
        _ctx: ServerRequestContext,
        _params: PaginatedRequestParams | None,
    ) -> ListToolsResult:
        return ListToolsResult(tools=build_tools())

    async def on_call_tool(
        _ctx: ServerRequestContext,
        params: CallToolRequestParams,
    ):
        return await dispatcher.dispatch(params.name, params.arguments)

    return Server(
        SERVER_NAME,
        version=SERVER_VERSION,
        on_list_tools=on_list_tools,
        on_call_tool=on_call_tool,
    )


def initialization_options(server: Server) -> InitializationOptions:
    return InitializationOptions(
        server_name=SERVER_NAME,
        server_version=SERVER_VERSION,
        capabilities=server.get_capabilities(
            notification_options=NotificationOptions(),
            experimental_capabilities={},
        ),
    )
