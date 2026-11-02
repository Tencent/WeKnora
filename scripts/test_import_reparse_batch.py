import contextlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import io
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
from urllib.error import HTTPError

import import_reparse_batch as importer


class ImportTests(unittest.TestCase):
    def manifest(self, count=3):
        return {"kb_id": "synthetic-kb", "entries": [
            {"id": f"doc-{n:05}", "knowledge_base_id": "synthetic-kb", "title": "private", "original_content": "must not send"}
            for n in range(count)]}

    def test_frozen_27328_members_and_stable_digest(self):
        manifest = self.manifest(27328)
        body = importer.registration(manifest, 27328)
        manifest["entries"].reverse()
        manifest["entries"].append(manifest["entries"][0])
        self.assertEqual(body, importer.registration(manifest, 27328))
        self.assertEqual(len(body["ids"]), 27328)
        self.assertEqual(body["mode"], "tracking_only")
        self.assertNotIn("private", json.dumps(body))
        self.assertNotIn("must not send", json.dumps(body))

    def test_reject_invalid_scope_count_and_ids(self):
        for manifest, expected in [
            (self.manifest(), 27328), (self.manifest(0), None),
            (self.manifest(50001), None),
            ({"kb_id": "kb", "entries": [{"id": "bad\nID"}]}, None),
            ({"kb_id": "kb", "entries": [{"id": "id", "knowledge_base_id": "other"}]}, None),
        ]:
            with self.assertRaises(ValueError):
                importer.registration(manifest, expected)

    def test_dry_run_performs_no_network_and_prints_no_ids(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "manifest.json"
            path.write_text(json.dumps(self.manifest()), encoding="utf-8")
            output = io.StringIO()
            with patch.object(importer, "register", side_effect=AssertionError("network forbidden")), contextlib.redirect_stdout(output):
                self.assertEqual(importer.main([str(path), "--dry-run", "--expected-count", "3"]), 0)
            result = json.loads(output.getvalue())
            self.assertTrue(result["dry_run"])
            self.assertNotIn("doc-", output.getvalue())

    def test_only_tracking_registration_request_and_dedup_on_server(self):
        requests = []
        batches = {}
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                requests.append((self.path, body, self.headers.get("X-API-Key")))
                batch = batches.setdefault(body["manifest_sha256"], "synthetic-batch")
                data = json.dumps({"success": True, "data": {"id": batch}}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            def log_message(self, *args):
                pass
        server = HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            body = importer.registration(self.manifest())
            url = f"http://127.0.0.1:{server.server_port}"
            self.assertEqual(importer.register(url, "synthetic-key", body), importer.register(url + "/api/v1", "synthetic-key", body))
        finally:
            server.shutdown()
            server.server_close()
            thread.join()
        self.assertEqual(len(batches), 1)
        self.assertEqual(len(requests), 2)
        for path, body, key in requests:
            self.assertEqual(path, "/api/v1/knowledge/reparse-batches")
            self.assertEqual(body["mode"], "tracking_only")
            self.assertEqual(key, "synthetic-key")

    def test_reject_url_credentials_and_unexpected_paths(self):
        for url in ("file:///tmp/test", "https://key@server", "https://server/knowledge/batch-reparse", "https://server?secret=1"):
            with self.assertRaises(ValueError):
                importer.register(url, "synthetic", {})

    def test_redirect_does_not_forward_api_key(self):
        redirected = []
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                self.send_response(307)
                self.send_header("Location", "/unexpected-destination")
                self.end_headers()
            def do_GET(self):
                redirected.append(self.path)
                self.send_response(200)
                self.end_headers()
            def log_message(self, *args):
                pass
        server = HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            with self.assertRaises(HTTPError) as caught:
                importer.register(f"http://127.0.0.1:{server.server_port}", "synthetic", importer.registration(self.manifest()))
            caught.exception.close()
            self.assertEqual(redirected, [])
        finally:
            server.shutdown()
            server.server_close()
            thread.join()


if __name__ == "__main__":
    unittest.main()
