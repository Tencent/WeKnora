#!/usr/bin/env python3
"""WeKnora MCP Server v3 - 全功能集成

基于 WeKnora CLI v0.3 的完整能力集，提供：
- 智能体对话（SSE 流式）
- 知识库管理（CRUD + 搜索）
- 文档管理（上传/下载/列表）
- 混合搜索（chunks/KB/docs/sessions）
- 原始 API 透传
"""

import asyncio
import json
import logging
import os
from typing import Optional

import httpx
from mcp.server import NotificationOptions, Server
from mcp.server.models import InitializationOptions
import mcp.server.stdio
import mcp.types as types

logging.basicConfig(level=logging.WARNING)
logger = logging.getLogger(__name__)

import sys

# 先加载 .env 文件（如果存在）
_env_file = os.path.join(os.path.dirname(__file__), ".env")
if os.path.exists(_env_file):
    with open(_env_file) as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                os.environ[k.strip()] = v.strip()

BASE_URL = os.getenv("WEKNORA_BASE_URL") or "http://localhost:8080/api/v1"
API_KEY = os.getenv("WEKNORA_API_KEY") or ""

if not API_KEY:
    print("[WeKnora MCP] 警告: WEKNORA_API_KEY 未设置，请配置 .env 文件", file=sys.stderr)
else:
    print(f"[WeKnora MCP] 启动 - API_KEY: {API_KEY[:8]}..., BASE_URL: {BASE_URL}", file=sys.stderr)

# 服务端状态（进程内持久化）
_state = {
    "agent": None,       # {"id", "name", "description", "knowledge_bases"}
    "session_id": None,
}


def _headers() -> dict:
    return {
        "X-API-Key": API_KEY,
        "Content-Type": "application/json",
    }


async def _request(method: str, path: str, **kwargs) -> httpx.Response:
    async with httpx.AsyncClient(timeout=60) as c:
        r = await c.request(method, f"{BASE_URL}{path}", headers=_headers(), **kwargs)
        return r


# ---------- 智能体管理 ----------

async def _fetch_agents() -> list:
    r = await _request("GET", "/agents")
    if r.status_code != 200:
        raise RuntimeError(f"获取智能体失败: HTTP {r.status_code}")
    data = r.json()
    agents = data if isinstance(data, list) else data.get("data", data.get("agents", []))
    return agents if isinstance(agents, list) else []


async def _fetch_agent(agent_id: str) -> Optional[dict]:
    r = await _request("GET", f"/agents/{agent_id}")
    if r.status_code != 200:
        return None
    data = r.json()
    return data if isinstance(data, dict) and "id" in data else data.get("data", {})


def _format_agent_list(agents: list) -> str:
    if not agents:
        return "没有可用的智能体"
    lines = []
    for a in agents:
        lines.append(f"- **{a.get('name', '?')}**  ID: `{a.get('id', '?')}`\n  {a.get('description', '')}")
    return "\n\n".join(lines)


# ---------- 会话管理 ----------

async def _create_session() -> str:
    agent_name = _state["agent"]["name"] if _state["agent"] else "default"
    r = await _request("POST", "/sessions", json={"name": f"cc-{agent_name}"})
    if r.status_code in (200, 201):
        data = r.json()
        return data.get("id") or data.get("data", {}).get("id")
    raise RuntimeError(f"创建会话失败: {r.status_code} {r.text[:200]}")


# ---------- SSE 流式对话 ----------

async def _do_agent_chat(message: str) -> str:
    payload = {
        "query": message,
        "agent_id": _state["agent"]["id"],
    }

    async with httpx.AsyncClient(timeout=httpx.Timeout(300, connect=30)) as c:
        async with c.stream(
            "POST",
            f"{BASE_URL}/agent-chat/{_state['session_id']}",
            headers=_headers(),
            json=payload,
        ) as resp:
            if resp.status_code != 200:
                body = await resp.aread()
                return f"对话失败: HTTP {resp.status_code} {body.decode()[:500]}"

            answer_parts = []
            last_error = None
            async for line in resp.aiter_lines():
                line = line.strip()
                if not line or not line.startswith("data:"):
                    continue
                payload_str = line[5:].strip()
                if not payload_str or payload_str == "[DONE]":
                    continue
                try:
                    event = json.loads(payload_str)
                except json.JSONDecodeError:
                    continue

                rtype = event.get("response_type", "")
                content = event.get("content", "")
                if rtype == "answer" and content:
                    answer_parts.append(content)
                    last_error = None
                elif rtype == "final_answer" and content:
                    answer_parts.append(content)
                    last_error = None
                elif rtype in ("thinking", "thought") and content:
                    pass
                elif rtype == "error" and content:
                    last_error = content

            if answer_parts:
                return "".join(answer_parts)
            if last_error:
                return f"智能体返回错误: {last_error}"
            return "（智能体未返回有效内容）"


# =====================================================
#  MCP 工具定义
# =====================================================

app = Server("weknora-server")


@app.list_tools()
async def handle_list_tools() -> list[types.Tool]:
    return [
        # --- 智能体对话 ---
        types.Tool(
            name="weknora_chat",
            description=(
                "与WeKnora智能体对话。首次使用会提示选择智能体，之后自动记住选择。"
                "用户可以说'切换智能体'来重新选择，说'新建对话'来开启新的会话。"
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "message": {"type": "string", "description": "要发送给智能体的问题或消息"},
                    "new_session": {"type": "boolean", "description": "是否新建对话，用户说'新建对话'时设为true"},
                },
                "required": ["message"],
            },
        ),
        types.Tool(
            name="weknora_select_agent",
            description="选择或切换WeKnora智能体。传入智能体ID完成选择。",
            inputSchema={
                "type": "object",
                "properties": {
                    "agent_id": {"type": "string", "description": "智能体ID（从 weknora_list_agents 获取）"},
                },
                "required": ["agent_id"],
            },
        ),
        types.Tool(
            name="weknora_list_agents",
            description="列出所有可用的WeKnora智能体，返回名称、ID和描述。",
            inputSchema={"type": "object", "properties": {}},
        ),
        types.Tool(
            name="weknora_current_agent",
            description="查看当前选中的智能体信息。",
            inputSchema={"type": "object", "properties": {}},
        ),

        # --- 知识库管理 ---
        types.Tool(
            name="weknora_kb_list",
            description="列出所有知识库，返回名称、ID、文档数量、描述等信息。",
            inputSchema={"type": "object", "properties": {}},
        ),
        types.Tool(
            name="weknora_kb_view",
            description="查看单个知识库的详细信息，包括配置、文档数等。",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "知识库ID"},
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="weknora_kb_create",
            description="创建新知识库。",
            inputSchema={
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "知识库名称"},
                    "description": {"type": "string", "description": "知识库描述（可选）"},
                },
                "required": ["name"],
            },
        ),
        types.Tool(
            name="weknora_kb_delete",
            description="删除知识库（谨慎操作，不可恢复）。",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "要删除的知识库ID"},
                },
                "required": ["kb_id"],
            },
        ),

        # --- 文档管理 ---
        types.Tool(
            name="weknora_doc_list",
            description="列出指定知识库中的所有文档，包括名称、类型、状态等。",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "知识库ID"},
                },
                "required": ["kb_id"],
            },
        ),
        types.Tool(
            name="weknora_doc_view",
            description="查看指定文档的详细信息。",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "知识库ID"},
                    "doc_id": {"type": "string", "description": "文档ID"},
                },
                "required": ["kb_id", "doc_id"],
            },
        ),
        types.Tool(
            name="weknora_doc_delete",
            description="从知识库中删除指定文档。",
            inputSchema={
                "type": "object",
                "properties": {
                    "kb_id": {"type": "string", "description": "知识库ID"},
                    "doc_id": {"type": "string", "description": "文档ID"},
                },
                "required": ["kb_id", "doc_id"],
            },
        ),

        # --- 搜索 ---
        types.Tool(
            name="weknora_search_chunks",
            description="在知识库中进行混合搜索（向量+关键词），返回最相关的文本分块。用于精确检索知识内容。",
            inputSchema={
                "type": "object",
                "properties": {
                    "query": {"type": "string", "description": "搜索查询文本"},
                    "kb_ids": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "要搜索的知识库ID列表",
                    },
                    "top_k": {"type": "integer", "description": "返回结果数量（默认5）"},
                },
                "required": ["query", "kb_ids"],
            },
        ),

        # --- 会话管理 ---
        types.Tool(
            name="weknora_session_list",
            description="列出所有聊天会话。",
            inputSchema={"type": "object", "properties": {}},
        ),
        types.Tool(
            name="weknora_session_delete",
            description="删除指定聊天会话。",
            inputSchema={
                "type": "object",
                "properties": {
                    "session_id": {"type": "string", "description": "会话ID"},
                },
                "required": ["session_id"],
            },
        ),

        # --- 原始 API ---
        types.Tool(
            name="weknora_api",
            description="直接调用WeKnora REST API，用于没有专用工具的高级操作。自动附加认证头。",
            inputSchema={
                "type": "object",
                "properties": {
                    "method": {"type": "string", "description": "HTTP方法（GET/POST/PUT/DELETE）", "default": "GET"},
                    "path": {"type": "string", "description": "API路径（如 /knowledge-bases）"},
                    "body": {"type": "string", "description": "请求体JSON字符串（可选）"},
                },
                "required": ["path"],
            },
        ),
    ]


@app.call_tool()
async def handle_call_tool(
    name: str, arguments: dict | None
) -> list[types.TextContent | types.ImageContent | types.EmbeddedResource]:
    args = arguments or {}

    try:
        handler = {
            "weknora_chat": _handle_chat,
            "weknora_select_agent": _handle_select_agent,
            "weknora_list_agents": _handle_list_agents,
            "weknora_current_agent": _handle_current_agent,
            "weknora_kb_list": _handle_kb_list,
            "weknora_kb_view": _handle_kb_view,
            "weknora_kb_create": _handle_kb_create,
            "weknora_kb_delete": _handle_kb_delete,
            "weknora_doc_list": _handle_doc_list,
            "weknora_doc_view": _handle_doc_view,
            "weknora_doc_delete": _handle_doc_delete,
            "weknora_search_chunks": _handle_search_chunks,
            "weknora_session_list": _handle_session_list,
            "weknora_session_delete": _handle_session_delete,
            "weknora_api": _handle_api,
        }.get(name)

        if handler:
            return await handler(args)
        return [types.TextContent(type="text", text=f"未知工具: {name}")]
    except Exception as e:
        logger.error(f"工具执行失败 [{name}]: {e}")
        return [types.TextContent(type="text", text=f"错误: {e}")]


# =====================================================
#  工具实现
# =====================================================

# --- 智能体对话 ---

async def _handle_chat(args: dict) -> list:
    global _state
    message = args["message"]
    new_session = args.get("new_session", False)

    if not _state["agent"]:
        try:
            agents = await _fetch_agents()
        except Exception as e:
            return [types.TextContent(type="text", text=f"连接WeKnora失败: {e}\n请确认WeKnora服务正在运行")]

        if not agents:
            return [types.TextContent(type="text", text="WeKnora中没有可用的智能体，请先在WeKnora中创建智能体。")]

        agent_list = _format_agent_list(agents)
        return [types.TextContent(
            type="text",
            text=f"尚未选择智能体。请告诉我你想使用哪个智能体：\n\n{agent_list}\n\n请回复智能体名称或ID。",
        )]

    if new_session or not _state["session_id"]:
        _state["session_id"] = await _create_session()
        if new_session and message in ("新建对话", "新对话", "new chat", ""):
            return [types.TextContent(type="text", text=f"已开启新对话（智能体: {_state['agent']['name']}），请开始提问。")]

    answer = await _do_agent_chat(message)
    return [types.TextContent(type="text", text=answer)]


async def _handle_select_agent(args: dict) -> list:
    global _state
    agent_id = args["agent_id"]

    agent = await _fetch_agent(agent_id)
    if not agent:
        return [types.TextContent(type="text", text=f"智能体 {agent_id} 不存在或无法访问")]

    _state["agent"] = {
        "id": agent.get("id", agent_id),
        "name": agent.get("name", "Unknown"),
        "description": agent.get("description", ""),
        "knowledge_bases": agent.get("knowledge_bases", []),
    }
    _state["session_id"] = None

    kb_info = ""
    kbs = _state["agent"].get("knowledge_bases", [])
    if kbs:
        kb_names = [kb.get("name", kb.get("id", "?")) for kb in kbs]
        kb_info = f"\n关联知识库: {', '.join(kb_names)}"

    return [types.TextContent(
        type="text",
        text=f"已选择智能体: {_state['agent']['name']} (ID: {agent_id}){kb_info}\n现在可以开始对话了。",
    )]


async def _handle_list_agents(args: dict = None) -> list:
    try:
        agents = await _fetch_agents()
    except Exception as e:
        return [types.TextContent(type="text", text=f"获取智能体列表失败: {e}")]
    return [types.TextContent(type="text", text=_format_agent_list(agents))]


async def _handle_current_agent(args: dict = None) -> list:
    if _state["agent"]:
        a = _state["agent"]
        text = f"当前智能体: **{a['name']}** (ID: `{a['id']}`)\n描述: {a.get('description', '无')}"
        if a.get("knowledge_bases"):
            kb_names = [kb.get("name", "?") for kb in a["knowledge_bases"]]
            text += f"\n关联知识库: {', '.join(kb_names)}"
        return [types.TextContent(type="text", text=text)]
    return [types.TextContent(type="text", text="尚未选择智能体。使用 weknora_chat 开始对话会自动引导选择。")]


# --- 知识库管理 ---

async def _handle_kb_list(args: dict) -> list:
    r = await _request("GET", "/knowledge-bases")
    if r.status_code != 200:
        return [types.TextContent(type="text", text=f"获取知识库列表失败: HTTP {r.status_code}")]
    data = r.json()
    kbs = data if isinstance(data, list) else data.get("data", [])
    if not kbs:
        return [types.TextContent(type="text", text="没有知识库")]

    lines = []
    for kb in kbs:
        name = kb.get("name", "?")
        kb_id = kb.get("id", "?")
        doc_count = kb.get("knowledge_count", kb.get("document_count", 0))
        desc = kb.get("description", "")
        lines.append(f"- **{name}**  ID: `{kb_id}`  ({doc_count} 文档)\n  {desc}")
    return [types.TextContent(type="text", text="\n\n".join(lines))]


async def _handle_kb_view(args: dict) -> list:
    kb_id = args["kb_id"]
    r = await _request("GET", f"/knowledge-bases/{kb_id}")
    if r.status_code != 200:
        return [types.TextContent(type="text", text=f"获取知识库详情失败: HTTP {r.status_code}")]
    return [types.TextContent(type="text", text=json.dumps(r.json(), indent=2, ensure_ascii=False))]


async def _handle_kb_create(args: dict) -> list:
    body = {"name": args["name"]}
    if args.get("description"):
        body["description"] = args["description"]
    r = await _request("POST", "/knowledge-bases", json=body)
    if r.status_code not in (200, 201):
        return [types.TextContent(type="text", text=f"创建知识库失败: HTTP {r.status_code} {r.text[:300]}")]
    data = r.json()
    kb = data if isinstance(data, dict) and "id" in data else data.get("data", {})
    return [types.TextContent(type="text", text=f"知识库已创建: **{kb.get('name')}** (ID: `{kb.get('id')}`)")]


async def _handle_kb_delete(args: dict) -> list:
    kb_id = args["kb_id"]
    r = await _request("DELETE", f"/knowledge-bases/{kb_id}")
    if r.status_code not in (200, 204):
        return [types.TextContent(type="text", text=f"删除知识库失败: HTTP {r.status_code}")]
    return [types.TextContent(type="text", text=f"知识库 {kb_id} 已删除")]


# --- 文档管理 ---

async def _handle_doc_list(args: dict) -> list:
    kb_id = args["kb_id"]
    r = await _request("GET", f"/knowledge-bases/{kb_id}/knowledge")
    if r.status_code != 200:
        return [types.TextContent(type="text", text=f"获取文档列表失败: HTTP {r.status_code}")]
    data = r.json()
    docs = data if isinstance(data, list) else data.get("data", data.get("knowledge", []))
    if not docs:
        return [types.TextContent(type="text", text="该知识库没有文档")]

    lines = []
    for doc in docs[:50]:
        name = doc.get("file_name", doc.get("title", doc.get("name", "?")))
        doc_id = doc.get("id", "?")
        status = doc.get("parse_status", "?")
        ftype = doc.get("file_type", "")
        lines.append(f"- {name}  ID: `{doc_id}`  状态: {status}  类型: {ftype}")
    if len(docs) > 50:
        lines.append(f"\n... 共 {len(docs)} 个文档，仅显示前 50 个")
    return [types.TextContent(type="text", text="\n".join(lines))]


async def _handle_doc_view(args: dict) -> list:
    kb_id = args["kb_id"]
    doc_id = args["doc_id"]
    r = await _request("GET", f"/knowledge-bases/{kb_id}/knowledge/{doc_id}")
    if r.status_code != 200:
        return [types.TextContent(type="text", text=f"获取文档详情失败: HTTP {r.status_code}")]
    return [types.TextContent(type="text", text=json.dumps(r.json(), indent=2, ensure_ascii=False))]


async def _handle_doc_delete(args: dict) -> list:
    kb_id = args["kb_id"]
    doc_id = args["doc_id"]
    r = await _request("DELETE", f"/knowledge-bases/{kb_id}/knowledge/{doc_id}")
    if r.status_code not in (200, 204):
        return [types.TextContent(type="text", text=f"删除文档失败: HTTP {r.status_code}")]
    return [types.TextContent(type="text", text=f"文档 {doc_id} 已从知识库 {kb_id} 中删除")]


# --- 搜索 ---

async def _handle_search_chunks(args: dict) -> list:
    query = args["query"]
    kb_ids = args["kb_ids"]
    top_k = args.get("top_k", 5)

    r = await _request("POST", "/knowledge-search", json={
        "query": query,
        "knowledge_base_ids": kb_ids,
        "top_k": top_k,
    })
    if r.status_code != 200:
        return [types.TextContent(type="text", text=f"搜索失败: HTTP {r.status_code} {r.text[:200]}")]
    return [types.TextContent(type="text", text=json.dumps(r.json(), indent=2, ensure_ascii=False))]


# --- 会话管理 ---

async def _handle_session_list(args: dict) -> list:
    r = await _request("GET", "/sessions")
    if r.status_code != 200:
        return [types.TextContent(type="text", text=f"获取会话列表失败: HTTP {r.status_code}")]
    data = r.json()
    sessions = data if isinstance(data, list) else data.get("data", [])
    if not sessions:
        return [types.TextContent(type="text", text="没有会话")]

    lines = []
    for s in sessions[:30]:
        name = s.get("name", "?")
        sid = s.get("id", "?")
        updated = s.get("updated_at", s.get("created_at", ""))
        lines.append(f"- {name}  ID: `{sid}`  {updated}")
    return [types.TextContent(type="text", text="\n".join(lines))]


async def _handle_session_delete(args: dict) -> list:
    session_id = args["session_id"]
    r = await _request("DELETE", f"/sessions/{session_id}")
    if r.status_code not in (200, 204):
        return [types.TextContent(type="text", text=f"删除会话失败: HTTP {r.status_code}")]
    return [types.TextContent(type="text", text=f"会话 {session_id} 已删除")]


# --- 原始 API ---

async def _handle_api(args: dict) -> list:
    method = args.get("method", "GET").upper()
    path = args["path"]
    body = args.get("body")

    kwargs = {}
    if body:
        try:
            kwargs["json"] = json.loads(body)
        except json.JSONDecodeError:
            kwargs["content"] = body.encode()

    r = await _request(method, path, **kwargs)
    result = {"status": r.status_code, "path": path}
    try:
        result["body"] = r.json()
    except Exception:
        result["body"] = r.text[:2000]
    return [types.TextContent(type="text", text=json.dumps(result, indent=2, ensure_ascii=False))]


# ---------- 启动 ----------

async def run():
    async with mcp.server.stdio.stdio_server() as (read_stream, write_stream):
        await app.run(
            read_stream,
            write_stream,
            InitializationOptions(
                server_name="weknora-server",
                server_version="3.0.0",
                capabilities=app.get_capabilities(
                    notification_options=NotificationOptions(),
                    experimental_capabilities={},
                ),
            ),
        )


def main():
    asyncio.run(run())


if __name__ == "__main__":
    main()
