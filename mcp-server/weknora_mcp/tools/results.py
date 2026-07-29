"""Helpers for constructing MCP tool call results."""

from __future__ import annotations

import json
from typing import Any

from mcp.types import CallToolResult, TextContent


def tool_success(result: Any) -> CallToolResult:
    return CallToolResult(
        content=[
            TextContent(
                type="text",
                text=json.dumps(result, indent=2, ensure_ascii=False),
            )
        ],
        is_error=False,
    )


def tool_failure(message: str) -> CallToolResult:
    return CallToolResult(
        content=[TextContent(type="text", text=message)],
        is_error=True,
    )
