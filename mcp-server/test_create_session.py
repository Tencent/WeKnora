import asyncio
import unittest
from unittest import mock

import weknora_mcp_server as srv


class CreateSessionTest(unittest.TestCase):
    def test_mcp_schema_does_not_require_kb(self):
        tools = asyncio.run(srv.mcp.list_tools())
        tool = next(tool for tool in tools if tool.name == "create_session")
        self.assertIn("kb_id", tool.input_schema["properties"])
        self.assertNotIn("kb_id", tool.input_schema.get("required", []))

    def test_without_kb_only_posts_session_metadata(self):
        response = {"data": {"id": "new-session"}}
        with mock.patch.object(srv.client, "_request", return_value=response) as req:
            self.assertEqual(srv.create_session(title="Research"), response)
        req.assert_called_once_with("POST", "/sessions", json={"title": "Research"})

    def test_preserves_explicit_legacy_kb(self):
        kbs = {"data": [{"name": "Docs", "id": "kb-1"}]}
        with mock.patch.object(srv.client, "_request", side_effect=[kbs, {}]) as req:
            srv.create_session("Docs", max_rounds=8, description="legacy call")
        lookup, create = req.call_args_list
        self.assertEqual(lookup, mock.call("GET", "/knowledge-bases"))
        self.assertEqual(create.args, ("POST", "/sessions"))
        body = create.kwargs["json"]
        self.assertEqual(body["knowledge_base_id"], "kb-1")
        self.assertEqual(body["session_strategy"]["max_rounds"], 8)
        self.assertEqual(body["description"], "legacy call")
