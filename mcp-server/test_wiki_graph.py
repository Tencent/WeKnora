#!/usr/bin/env python3
"""Tests for MCP wiki_graph passthrough (issue #3177)."""

import sys
import types
import unittest
from unittest import mock

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


class WikiGraphClientTest(unittest.TestCase):
    def test_forwards_ego_params(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"nodes": []}) as req:
            client.wiki_graph(
                "kb-1",
                mode="ego",
                center="entity/acme",
                depth=2,
                limit=100,
                page_types="entity,concept",
            )
        self.assertEqual(req.call_args.args[0], "GET")
        self.assertEqual(
            req.call_args.args[1], "/knowledgebase/kb-1/wiki/graph"
        )
        params = req.call_args.kwargs["params"]
        self.assertEqual(params["mode"], "ego")
        self.assertEqual(params["center"], "entity/acme")
        self.assertEqual(params["depth"], 2)
        self.assertEqual(params["limit"], 100)
        self.assertEqual(params["types"], "entity,concept")

    def test_omits_empty_center_and_types(self):
        client = srv.WeKnoraClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"nodes": []}) as req:
            client.wiki_graph("kb-1")
        params = req.call_args.kwargs["params"]
        self.assertNotIn("center", params)
        self.assertNotIn("types", params)
        self.assertEqual(params["mode"], "overview")


class WikiGraphToolTest(unittest.TestCase):
    def test_clamps_depth_and_limit(self):
        with mock.patch.object(
            srv.client, "wiki_graph", return_value={"nodes": []}
        ) as method:
            srv.wiki_graph("kb-1", mode="overview", depth=99, limit=99999)
        kwargs = method.call_args.kwargs
        self.assertEqual(kwargs["depth"], 3)
        self.assertEqual(kwargs["limit"], 2000)

    def test_rejects_invalid_mode(self):
        with self.assertRaises(ValueError):
            srv.wiki_graph("kb-1", mode="nope")

    def test_requires_center_for_ego(self):
        with self.assertRaises(ValueError):
            srv.wiki_graph("kb-1", mode="ego")


if __name__ == "__main__":
    unittest.main()
