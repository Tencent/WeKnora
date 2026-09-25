#!/usr/bin/env python3
"""Tests for the create_knowledge_from_text MCP tool and client method.

unittest discover 会收集本文件中的 TestCase；也可直接运行:
python test_create_knowledge_from_text.py
"""

import unittest
from unittest import mock

import weknora_mcp_server as srv


class CreateKnowledgeFromTextTest(unittest.TestCase):
    def test_client_method_exists_and_callable(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        self.assertTrue(callable(getattr(client, "create_knowledge_from_text", None)))

    def test_posts_to_manual_endpoint_with_expected_body(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"ok": True}) as req:
            client.create_knowledge_from_text("kb-1", "T", "C", tag_ids=["t1"])
        args, kwargs = req.call_args
        self.assertEqual(args[0], "POST")
        self.assertEqual(args[1], "/knowledge-bases/kb-1/knowledge/manual")
        body = kwargs["json"]
        self.assertEqual(body["title"], "T")
        self.assertEqual(body["content"], "C")
        self.assertEqual(body["tag_ids"], ["t1"])
        # status defaults to "publish" so the entry is indexed/searchable.
        self.assertEqual(body["status"], "publish")

    def test_status_can_be_overridden_to_draft(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"ok": True}) as req:
            client.create_knowledge_from_text("kb-1", "T", "C", status="draft")
        self.assertEqual(req.call_args.kwargs["json"]["status"], "draft")

    def test_omits_tag_ids_when_none(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"ok": True}) as req:
            client.create_knowledge_from_text("kb-1", "T", "C")
        body = req.call_args.kwargs["json"]
        self.assertNotIn("tag_ids", body)
        self.assertEqual(body["status"], "publish")

    def test_tool_function_resolves_kb_id_and_forwards_status(self):
        with mock.patch.object(
            srv.client, "resolve_kb_id", return_value="uuid-1"
        ) as rid, mock.patch.object(
            srv.client, "create_knowledge_from_text", return_value={"ok": True}
        ) as method:
            srv.create_knowledge_from_text("my-kb", "T", "C", status="draft")
        rid.assert_called_once_with("my-kb")
        method.assert_called_once_with("uuid-1", "T", "C", tag_ids=None, status="draft")


if __name__ == "__main__":
    unittest.main()
