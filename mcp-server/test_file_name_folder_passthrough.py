#!/usr/bin/env python3
"""Tests for MCP file_name / folder_path passthrough (issue #3173)."""

import sys
import types
import unittest
from unittest import mock

# Lightweight stub so unit tests can import the client module without the
# full MCP SDK installed (CI installs real deps; this only covers the HTTP client).
if "mcp" not in sys.modules:
    mcp_pkg = types.ModuleType("mcp")
    mcp_server = types.ModuleType("mcp.server")

    class _MCPServer:
        def __init__(self, *args, **kwargs):
            pass

        def tool(self):
            def decorator(fn):
                return fn

            return decorator

    mcp_server.MCPServer = _MCPServer
    mcp_pkg.server = mcp_server
    sys.modules["mcp"] = mcp_pkg
    sys.modules["mcp.server"] = mcp_server

import weknora_mcp_server as srv  # noqa: E402


class CreateKnowledgeFromFileTest(unittest.TestCase):
    def test_includes_file_name_in_multipart_when_set(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        payload = b"pdf-bytes"
        with mock.patch(
            "builtins.open", mock.mock_open(read_data=payload)
        ), mock.patch.object(
            srv, "resolve_upload_file_path", return_value="/safe/doc.pdf"
        ), mock.patch.object(
            srv.requests, "post"
        ) as post:
            post.return_value = mock.Mock(
                raise_for_status=lambda: None,
                json=lambda: {"id": "k1"},
            )
            client.create_knowledge_from_file(
                "kb-1", "doc.pdf", enable_multimodel=True, file_name="docs/spec/doc.pdf"
            )
        data = post.call_args.kwargs["data"]
        self.assertEqual(data["fileName"], "docs/spec/doc.pdf")
        self.assertEqual(data["enable_multimodel"], "true")

    def test_omits_file_name_when_empty(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch(
            "builtins.open", mock.mock_open(read_data=b"x")
        ), mock.patch.object(
            srv, "resolve_upload_file_path", return_value="/safe/doc.pdf"
        ), mock.patch.object(
            srv.requests, "post"
        ) as post:
            post.return_value = mock.Mock(
                raise_for_status=lambda: None,
                json=lambda: {"id": "k1"},
            )
            client.create_knowledge_from_file("kb-1", "doc.pdf")
        self.assertNotIn("fileName", post.call_args.kwargs["data"])


class ListKnowledgeFolderTest(unittest.TestCase):
    def test_forwards_folder_path_and_scope(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"items": []}) as req:
            client.list_knowledge(
                "kb-1", page=2, page_size=10, folder_path="docs", folder_scope="all"
            )
        params = req.call_args.kwargs["params"]
        self.assertEqual(params["page"], 2)
        self.assertEqual(params["page_size"], 10)
        self.assertEqual(params["folder_path"], "docs")
        self.assertEqual(params["folder_scope"], "all")

    def test_omits_folder_params_when_unset(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"items": []}) as req:
            client.list_knowledge("kb-1")
        params = req.call_args.kwargs["params"]
        self.assertNotIn("folder_path", params)
        self.assertNotIn("folder_scope", params)


if __name__ == "__main__":
    unittest.main()
