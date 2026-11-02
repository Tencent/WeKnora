#!/usr/bin/env python3
"""Tests for the tag tooling: list_tags, and tag_ids on chat / agent_chat.

Covers the request shapes only — endpoint URLs, query parameters and JSON
bodies — by mocking WeKnoraClient._request and WeKnoraClient._consume_sse_stream.
Run with: python -m unittest test_tag_tools -v
"""

import unittest
from unittest import mock

import weknora_mcp_server
from weknora_mcp_server import WeKnoraClient


class ListTagsClientTest(unittest.TestCase):
    def setUp(self):
        self.client = WeKnoraClient("http://localhost:8080/api/v1", "test_key")

    def test_request_path_and_pagination(self):
        with mock.patch.object(self.client, "_request") as req:
            req.return_value = {"success": True, "data": []}
            self.client.list_tags("kb-uuid", page=2, page_size=10)
            req.assert_called_once_with(
                "GET",
                "/knowledge-bases/kb-uuid/tags",
                params={"page": 2, "page_size": 10},
            )

    def test_keyword_included_only_when_set(self):
        with mock.patch.object(self.client, "_request") as req:
            req.return_value = {"success": True, "data": []}
            self.client.list_tags("kb-uuid", keyword="region")
            self.assertEqual(
                req.call_args.kwargs["params"]["keyword"],
                "region",
            )

            self.client.list_tags("kb-uuid", keyword="")
            self.assertNotIn(
                "keyword",
                req.call_args.kwargs["params"],
            )

    def test_default_arguments(self):
        with mock.patch.object(self.client, "_request") as req:
            req.return_value = {"success": True, "data": []}
            self.client.list_tags("kb-uuid")
            self.assertEqual(
                req.call_args.kwargs["params"],
                {"page": 1, "page_size": 50},
            )


class ChatTagIdsTest(unittest.TestCase):
    def setUp(self):
        self.client = WeKnoraClient("http://localhost:8080/api/v1", "test_key")

    def _sse_result(self):
        return {"answer": "ok", "references": [], "_debug_events": []}

    def test_chat_body_includes_tag_ids(self):
        with mock.patch.object(
            self.client, "_consume_sse_stream", return_value=self._sse_result()
        ) as sse:
            self.client.chat(
                "sess-1",
                "question",
                knowledge_base_ids=["kb-uuid"],
                tag_ids=["tag-1", "tag-2"],
            )
            body = sse.call_args.args[1] if sse.call_args.args else sse.call_args.kwargs.get("body")
            self.assertEqual(body.get("tag_ids"), ["tag-1", "tag-2"])
            self.assertEqual(body.get("knowledge_base_ids"), ["kb-uuid"])

    def test_chat_body_omits_tag_ids_when_absent(self):
        with mock.patch.object(
            self.client, "_consume_sse_stream", return_value=self._sse_result()
        ) as sse:
            self.client.chat("sess-1", "question", knowledge_base_ids=["kb-uuid"])
            body = sse.call_args.args[1] if sse.call_args.args else sse.call_args.kwargs.get("body")
            self.assertNotIn("tag_ids", body)

    def test_agent_chat_body_includes_tag_ids(self):
        with mock.patch.object(
            self.client, "_consume_sse_stream", return_value=self._sse_result()
        ) as sse:
            self.client.agent_chat(
                "sess-1",
                "question",
                "agent-uuid",
                knowledge_base_ids=["kb-uuid"],
                tag_ids=["tag-1"],
            )
            body = sse.call_args.args[1] if sse.call_args.args else sse.call_args.kwargs.get("body")
            self.assertEqual(body.get("tag_ids"), ["tag-1"])
            self.assertEqual(body.get("agent_id"), "agent-uuid")


class TagToolRegistrationTest(unittest.TestCase):
    def test_client_and_tool_exist(self):
        client = WeKnoraClient("http://localhost:8080/api/v1", "test")
        self.assertTrue(callable(getattr(client, "list_tags", None)))

        # The @mcp.tool() decorator runs at import time; both chat tools must
        # expose a tag_ids parameter for MCP clients to discover the feature.
        import inspect

        for tool_name in ("chat", "agent_chat", "list_tags"):
            func = getattr(weknora_mcp_server, tool_name, None)
            self.assertTrue(callable(func), f"module-level {tool_name} missing")
        sig = inspect.signature(weknora_mcp_server.chat)
        self.assertIn("tag_ids", sig.parameters)
        sig = inspect.signature(weknora_mcp_server.agent_chat)
        self.assertIn("tag_ids", sig.parameters)
        sig = inspect.signature(weknora_mcp_server.list_tags)
        self.assertIs(sig.parameters["kb_id"].default, inspect.Parameter.empty)
        self.assertEqual(sig.parameters["keyword"].default, "")
        self.assertEqual(sig.parameters["page"].default, 1)

    def test_list_tags_tool_resolves_kb_name(self):
        fake_client = mock.MagicMock()
        fake_client.resolve_kb_id.return_value = "kb-uuid"
        fake_client.list_tags.return_value = {"success": True, "data": []}
        with mock.patch.object(weknora_mcp_server, "client", fake_client):
            result = weknora_mcp_server.list_tags(
                "my-knowledge-base", keyword="region", page=2, page_size=10
            )
        fake_client.resolve_kb_id.assert_called_once_with("my-knowledge-base")
        fake_client.list_tags.assert_called_once_with(
            "kb-uuid", page=2, page_size=10, keyword="region"
        )
        self.assertEqual(result, {"success": True, "data": []})


if __name__ == "__main__":
    unittest.main()
