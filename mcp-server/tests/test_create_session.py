import asyncio

import weknora_mcp_server as srv


def test_mcp_schema_does_not_require_kb():
    tools = asyncio.run(srv.mcp.list_tools())
    schema = next(tool.input_schema for tool in tools if tool.name == "create_session")
    assert "kb_id" in schema["properties"]
    assert "kb_id" not in schema.get("required", [])


def test_create_session_without_kb_only_posts_session_metadata(monkeypatch):
    calls = []

    def request(method, path, **kwargs):
        calls.append((method, path, kwargs))
        return {"data": {"id": "new-session"}}

    monkeypatch.setattr(srv.client, "_request", request)
    assert srv.create_session(title="Research") == {"data": {"id": "new-session"}}
    assert calls == [("POST", "/sessions", {"json": {"title": "Research"}})]


def test_create_session_preserves_explicit_legacy_kb(monkeypatch):
    posts = []

    def request(method, path, **kwargs):
        if (method, path) == ("GET", "/knowledge-bases"):
            return {"data": [{"name": "Docs", "id": "kb-1"}]}
        assert (method, path) == ("POST", "/sessions")
        posts.append(kwargs["json"])
        return {"success": True}

    monkeypatch.setattr(srv.client, "_request", request)
    srv.create_session("Docs", max_rounds=8, description="legacy call")
    assert posts[0]["knowledge_base_id"] == "kb-1"
    assert posts[0]["session_strategy"]["max_rounds"] == 8
    assert posts[0]["description"] == "legacy call"
