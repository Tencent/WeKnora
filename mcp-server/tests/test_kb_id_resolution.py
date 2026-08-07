from weknora_mcp_server import WeKnoraClient, _normalize_kb_entries

# Shapes mirror GET /knowledge-bases and GET /shared-knowledge-bases wire JSON.
# See internal/handler/knowledgebase.go (buildKBListResponse) and
# internal/handler/organization.go (sharedKBRow / ListSharedKnowledgeBases).


def test_normalize_kb_entries_flattens_shared_rows():
    resp = {
        "success": True,
        "data": [
            {
                "knowledge_base": {"id": "kb-shared-1", "name": "Shared Docs"},
                "share_id": "kbs-1",
                "organization_id": "org-1",
                "permission": "viewer",
            }
        ],
        "total": 1,
    }
    assert _normalize_kb_entries(resp) == [
        {"id": "kb-shared-1", "name": "Shared Docs"}
    ]


def test_normalize_kb_entries_passes_through_owned_rows():
    resp = {
        "success": True,
        "data": [{"id": "kb-owned-1", "name": "My Docs"}],
    }
    assert _normalize_kb_entries(resp) == [{"id": "kb-owned-1", "name": "My Docs"}]


def test_resolve_kb_id_accepts_shared_kb_name(monkeypatch):
    client = WeKnoraClient("http://example.test/api/v1", "")

    def fake_request(method, path, **kwargs):
        if path == "/knowledge-bases":
            return {"success": True, "data": []}
        if path == "/shared-knowledge-bases":
            return {
                "success": True,
                "data": [
                    {
                        "knowledge_base": {
                            "id": "kb-shared-1",
                            "name": "技术文档库",
                        },
                        "share_id": "kbs-1",
                    }
                ],
            }
        raise AssertionError(f"unexpected request: {method} {path}")

    monkeypatch.setattr(client, "_request", fake_request)

    assert client.resolve_kb_id("技术文档库") == "kb-shared-1"


def test_resolve_kb_id_prefers_owned_over_shared_name(monkeypatch):
    client = WeKnoraClient("http://example.test/api/v1", "")

    def fake_request(method, path, **kwargs):
        if path == "/knowledge-bases":
            return {
                "success": True,
                "data": [{"id": "kb-owned-1", "name": "Same Name"}],
            }
        if path == "/shared-knowledge-bases":
            return {
                "success": True,
                "data": [
                    {
                        "knowledge_base": {
                            "id": "kb-shared-1",
                            "name": "Same Name",
                        },
                        "share_id": "kbs-1",
                    }
                ],
            }
        raise AssertionError(f"unexpected request: {method} {path}")

    monkeypatch.setattr(client, "_request", fake_request)

    assert client.resolve_kb_id("same name") == "kb-owned-1"
