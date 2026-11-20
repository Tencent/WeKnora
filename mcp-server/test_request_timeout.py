#!/usr/bin/env python3
"""Tests for timeouts on the non-chat WeKnora API requests.

``WeKnoraClient._request`` and the file-upload ``requests.post`` used to
pass no ``timeout`` at all, so a hung backend blocked the calling MCP
tool indefinitely (``requests`` waits forever by default). The chat SSE
path already passes ``timeout=(10, WEKNORA_CHAT_TIMEOUT)``; these tests
pin the equivalent discipline for the plain JSON requests.

unittest discover 会收集本文件中的 TestCase；也可直接运行:
python test_request_timeout.py
"""

import os
import subprocess
import sys
import unittest
from pathlib import Path
from unittest import mock

import weknora_mcp_server as srv

MCP_SERVER_DIR = Path(__file__).resolve().parent


class RequestTimeoutTest(unittest.TestCase):
    def test_request_passes_default_timeout(self):
        client = srv.WeKnoraClient("http://example.test/api/v1", "k")
        with mock.patch.object(client.session, "request") as req:
            req.return_value.json.return_value = {"ok": True}
            client._request("GET", "/tenants")
        self.assertEqual(req.call_args.kwargs.get("timeout"), srv.WEKNORA_API_TIMEOUT)

    def test_request_respects_caller_supplied_timeout(self):
        client = srv.WeKnoraClient("http://example.test/api/v1", "k")
        with mock.patch.object(client.session, "request") as req:
            req.return_value.json.return_value = {"ok": True}
            client._request("GET", "/tenants", timeout=7)
        self.assertEqual(req.call_args.kwargs.get("timeout"), 7)

    def test_upload_post_passes_timeout(self):
        client = srv.WeKnoraClient("http://example.test/api/v1", "k")
        with mock.patch.object(
            srv, "resolve_upload_file_path", return_value="/tmp/x.pdf"
        ), mock.patch(
            "builtins.open", mock.mock_open(read_data=b"x")
        ), mock.patch.object(
            srv.requests, "post"
        ) as post:
            post.return_value = mock.Mock(
                raise_for_status=lambda: None, json=lambda: {"id": "k1"}
            )
            client.create_knowledge_from_file("kb-1", "x.pdf")
        self.assertEqual(post.call_args.kwargs.get("timeout"), srv.WEKNORA_API_TIMEOUT)


def _import_api_timeout(env_value):
    """Import the server module in a subprocess with WEKNORA_API_TIMEOUT set."""
    env = dict(os.environ)
    if env_value is None:
        env.pop("WEKNORA_API_TIMEOUT", None)
    else:
        env["WEKNORA_API_TIMEOUT"] = env_value
    result = subprocess.run(
        [
            sys.executable,
            "-c",
            "import weknora_mcp_server as s; print(s.WEKNORA_API_TIMEOUT)",
        ],
        capture_output=True,
        text=True,
        timeout=30,
        cwd=MCP_SERVER_DIR,
        env=env,
    )
    assert result.returncode == 0, result.stderr
    return int(result.stdout.strip())


class ApiTimeoutEnvParsingTest(unittest.TestCase):
    def test_defaults_to_300_when_unset(self):
        self.assertEqual(_import_api_timeout(None), 300)

    def test_uses_env_value_when_valid(self):
        self.assertEqual(_import_api_timeout("45"), 45)

    def test_falls_back_to_300_when_invalid(self):
        self.assertEqual(_import_api_timeout("abc"), 300)


if __name__ == "__main__":
    unittest.main()
