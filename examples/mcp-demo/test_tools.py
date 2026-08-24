#!/usr/bin/env python3
"""列出 MCP Demo 暴露的工具（需先启动 server.py）。"""

from __future__ import annotations

import asyncio
import os
import sys

import httpx
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client


async def main() -> int:
    base = os.getenv("MCP_DEMO_URL", "http://127.0.0.1:8010/mcp")
    token = os.getenv("MCP_SERVER_AUTH_TOKEN", "weknora-demo-token")

    async with httpx.AsyncClient(
        headers={"Authorization": f"Bearer {token}"},
        timeout=30.0,
    ) as http_client:
        async with streamable_http_client(base, http_client=http_client) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                tools = await session.list_tools()
                print(f"connected: {base}")
                print(f"tools ({len(tools.tools)}):")
                for tool in tools.tools:
                    print(f"  - {tool.name}: {tool.description}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(asyncio.run(main()))
    except Exception as exc:  # noqa: BLE001
        print(f"failed: {exc}", file=sys.stderr)
        print("start the server first: ./start.sh", file=sys.stderr)
        raise SystemExit(1) from exc
