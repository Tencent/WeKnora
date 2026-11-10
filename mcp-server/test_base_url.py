"""HTTP regressions for the legacy MCP client's configurable API base URL."""

import json
import os
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest import mock
from urllib.parse import urlsplit

import weknora_mcp_server as srv


class BaseURLTest(unittest.TestCase):
    def test_requests_keep_the_api_prefix_with_or_without_a_trailing_slash(self):
        listing = {"data": [{"id": "agent-1", "name": "Research"}]}
        uploaded = {"data": {"id": "knowledge-1"}}
        stream = (
            'data: {"response_type":"answer","content":"hello"}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        routes = {}
        for prefix in ("/api/v1", "/weknora/api/v1"):
            routes[("GET", prefix + "/agents")] = (
                "application/json", json.dumps(listing).encode()
            )
            for endpoint in ("/knowledge-chat/session-1", "/agent-chat/session-1"):
                routes[("POST", prefix + endpoint)] = ("text/event-stream", stream)
            routes[("POST", prefix + "/knowledge-bases/kb-1/knowledge/file")] = (
                "application/json", json.dumps(uploaded).encode()
            )

        class Handler(BaseHTTPRequestHandler):
            def handle_request(self):
                self.rfile.read(int(self.headers.get("Content-Length", "0")))
                route = routes.get((self.command, urlsplit(self.path).path))
                if route is None:
                    self.send_error(404)
                    return
                content_type, body = route
                self.send_response(200)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            do_GET = handle_request
            do_POST = handle_request

            def log_message(self, *args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as directory:
                document = Path(directory, "note.txt")
                document.write_text("knowledge", encoding="utf-8")
                with mock.patch.dict(
                    os.environ,
                    {
                        "MCP_ALLOWED_UPLOAD_DIRS": directory,
                        "NO_PROXY": "127.0.0.1",
                        "no_proxy": "127.0.0.1",
                    },
                ):
                    for prefix in ("/api/v1", "/weknora/api/v1"):
                        for suffix in ("", "/"):
                            base_url = f"http://127.0.0.1:{server.server_port}{prefix}{suffix}"
                            client = srv.WeKnoraClient(base_url, "test-key")
                            try:
                                with self.subTest(prefix=prefix, suffix=suffix, api="list"):
                                    self.assertEqual(client.list_agents(), listing)
                                with self.subTest(prefix=prefix, suffix=suffix, api="chat"):
                                    self.assertEqual(
                                        client.chat("session-1", "question")["answer"], "hello"
                                    )
                                with self.subTest(prefix=prefix, suffix=suffix, api="agent"):
                                    self.assertEqual(
                                        client.agent_chat("session-1", "question", "agent-1")["answer"],
                                        "hello",
                                    )
                                with self.subTest(prefix=prefix, suffix=suffix, api="upload"):
                                    self.assertEqual(
                                        client.create_knowledge_from_file("kb-1", str(document)),
                                        uploaded,
                                    )
                            finally:
                                client.session.close()
        finally:
            server.shutdown()
            thread.join()
            server.server_close()


if __name__ == "__main__":
    unittest.main()
