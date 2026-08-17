#!/usr/bin/env python3
"""
WeKnora MCP Server 模组测试脚本

测试模组的各种启动方式和功能。unittest discover 会收集本文件中的 TestCase；
也可直接运行: python test_module.py
"""

import os
import subprocess
import sys
import unittest
from pathlib import Path

MCP_SERVER_DIR = Path(__file__).resolve().parent

REQUIRED_FILES = [
    "__init__.py",
    "main.py",
    "run_server.py",
    "weknora_mcp_server.py",
    "requirements.txt",
    "setup.py",
    "pyproject.toml",
    "README.md",
    "INSTALL.md",
    "LICENSE",
    "MANIFEST.in",
]


class ModuleIntegrationTest(unittest.TestCase):
    def test_imports(self):
        import mcp  # noqa: F401
        import requests  # noqa: F401
        import weknora_mcp_server  # noqa: F401
        from weknora_mcp_server import WeKnoraClient, run  # noqa: F401
        import main  # noqa: F401

    def test_environment_optional_vars(self):
        os.getenv("WEKNORA_BASE_URL")
        os.getenv("WEKNORA_API_KEY")

    def test_client_creation(self):
        from weknora_mcp_server import WeKnoraClient

        base_url = os.getenv("WEKNORA_BASE_URL", "http://localhost:8080/api/v1")
        api_key = os.getenv("WEKNORA_API_KEY", "test_key")
        client = WeKnoraClient(base_url, api_key)
        self.assertEqual(client.base_url, base_url)
        self.assertEqual(client.api_key, api_key)

    def test_required_files_exist(self):
        missing = [name for name in REQUIRED_FILES if not (MCP_SERVER_DIR / name).exists()]
        self.assertEqual(missing, [], f"Missing files: {missing}")

    def test_main_help(self):
        result = subprocess.run(
            [sys.executable, "main.py", "--help"],
            capture_output=True,
            text=True,
            timeout=10,
            cwd=MCP_SERVER_DIR,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_main_check_only(self):
        result = subprocess.run(
            [sys.executable, "main.py", "--check-only"],
            capture_output=True,
            text=True,
            timeout=10,
            cwd=MCP_SERVER_DIR,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_wiki_tools(self):
        import weknora_mcp_server

        client = weknora_mcp_server.WeKnoraClient("http://localhost:8080/api/v1", "test")
        for method in ["wiki_search", "wiki_read_page", "wiki_list_source_chunks", "wiki_index_view"]:
            self.assertTrue(hasattr(client, method), f"WeKnoraClient missing: {method}")
            self.assertTrue(callable(getattr(client, method)), f"{method} not callable")

    def test_wiki_search_envelope_strips_associate_fields(self):
        from weknora_mcp_server import WeKnoraClient

        pages = [{"id": "p1", "slug": "entity/acme", "title": "Acme"}]
        payload = {
            "query": "what is acme",
            "tree": [{"path": "公司", "leaves": [{"slug": "entity/acme"}]}],
            "pages": pages,
        }
        out = WeKnoraClient._wiki_search_pages_envelope(payload)
        self.assertEqual(list(out.keys()), ["pages"])
        self.assertEqual(out["pages"], pages)

    def test_wiki_search_envelope_unwraps_data(self):
        from weknora_mcp_server import WeKnoraClient

        pages = [{"slug": "concept/rag"}]
        out = WeKnoraClient._wiki_search_pages_envelope({"data": {"query": "q", "pages": pages}})
        self.assertEqual(out, {"pages": pages})

    def test_wiki_search_envelope_falls_back_to_tree_leaves(self):
        from weknora_mcp_server import WeKnoraClient

        payload = {
            "tree": [
                {
                    "path": "公司",
                    "leaves": [{"slug": "entity/acme"}],
                    "children": [
                        {"path": "公司/产品", "leaves": [{"slug": "entity/widget"}]}
                    ],
                }
            ]
        }
        out = WeKnoraClient._wiki_search_pages_envelope(payload)
        self.assertEqual(
            out,
            {"pages": [{"slug": "entity/acme"}, {"slug": "entity/widget"}]},
        )

    def test_wiki_search_posts_optional_prompt(self):
        from unittest.mock import Mock

        from weknora_mcp_server import WeKnoraClient

        client = WeKnoraClient("http://localhost:8080/api/v1", "test")
        client.resolve_kb_id = Mock(return_value="kb-1")
        client._request = Mock(return_value={"pages": [{"slug": "concept/a"}]})

        out = client.wiki_search("kb-1", "写产品说明", 8, "只选会改变卖点表述的叶子")
        self.assertEqual(out, {"pages": [{"slug": "concept/a"}]})
        client._request.assert_called_once_with(
            "POST",
            "/knowledgebase/kb-1/wiki/associate",
            json={
                "q": "写产品说明",
                "limit": 8,
                "prompt": "只选会改变卖点表述的叶子",
            },
        )

        client._request.reset_mock()
        client.wiki_search("kb-1", "写产品说明")
        client._request.assert_called_once_with(
            "POST",
            "/knowledgebase/kb-1/wiki/associate",
            json={"q": "写产品说明", "limit": 10},
        )

    def test_wiki_list_source_chunks_gets_slug_path(self):
        from unittest.mock import Mock

        from weknora_mcp_server import WeKnoraClient

        client = WeKnoraClient("http://localhost:8080/api/v1", "test")
        client.resolve_kb_id = Mock(return_value="kb-1")
        payload = {
            "slug": "concept/root-crack",
            "chunks": [{"id": "c1", "content": "原文"}],
            "chunk_ref_count": 1,
        }
        client._request = Mock(return_value=payload)

        out = client.wiki_list_source_chunks("手册库", "concept/root-crack")
        self.assertEqual(out, payload)
        client._request.assert_called_once_with(
            "GET",
            "/knowledgebase/kb-1/wiki/source-chunks/concept/root-crack",
        )

    def test_pyproject_metadata(self):
        text = (MCP_SERVER_DIR / "pyproject.toml").read_text(encoding="utf-8")
        self.assertIn("tencent-weknora-mcp", text)
        self.assertIn("weknora-mcp-server", text)


if __name__ == "__main__":
    unittest.main(verbosity=2)
