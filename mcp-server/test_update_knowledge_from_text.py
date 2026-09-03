#!/usr/bin/env python3
"""Tests for the update_knowledge_from_text MCP tool and client method."""

import unittest
from unittest import mock

import weknora_mcp_server as srv


class UpdateKnowledgeFromTextTest(unittest.TestCase):
    def test_client_puts_manual_update_to_expected_endpoint(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request") as req:
            req.return_value = {"ok": True}
            result = client.update_knowledge_from_text(
                "knowledge-1", "# Updated", title="New title"
            )

        self.assertEqual(result, {"ok": True})
        req.assert_called_once_with(
            "PUT",
            "/knowledge/manual/knowledge-1",
            json={
                "title": "New title",
                "content": "# Updated",
                "status": "publish",
            },
        )

    def test_client_can_save_a_draft_and_keep_the_title(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request") as req:
            req.return_value = {"ok": True}
            client.update_knowledge_from_text(
                "knowledge-1", "Draft content", status="draft"
            )

        self.assertEqual(
            req.call_args.kwargs["json"],
            {"title": "", "content": "Draft content", "status": "draft"},
        )

    def test_tool_forwards_all_update_fields(self):
        with mock.patch.object(
            srv.client, "update_knowledge_from_text", return_value={"ok": True}
        ) as method:
            result = srv.update_knowledge_from_text(
                "knowledge-1", "Body", title="Title", status="draft"
            )

        self.assertEqual(result, {"ok": True})
        method.assert_called_once_with(
            "knowledge-1", "Body", title="Title", status="draft"
        )


if __name__ == "__main__":
    unittest.main()
