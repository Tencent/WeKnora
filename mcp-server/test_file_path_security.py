#!/usr/bin/env python3
import os
import tempfile
import unittest

from upload_paths import clear_active_transport, resolve_upload_file_path, set_active_transport


class ResolveUploadFilePathTest(unittest.TestCase):
    def setUp(self):
        self._old_transport = os.environ.get("MCP_TRANSPORT")
        self._old_roots = os.environ.get("MCP_ALLOWED_UPLOAD_DIRS")
        os.environ.pop("MCP_TRANSPORT", None)

    def tearDown(self):
        clear_active_transport()
        if self._old_transport is None:
            os.environ.pop("MCP_TRANSPORT", None)
        else:
            os.environ["MCP_TRANSPORT"] = self._old_transport
        if self._old_roots is None:
            os.environ.pop("MCP_ALLOWED_UPLOAD_DIRS", None)
        else:
            os.environ["MCP_ALLOWED_UPLOAD_DIRS"] = self._old_roots

    def test_network_transport_blocks_path_outside_cwd(self):
        os.environ["MCP_TRANSPORT"] = "http"
        with tempfile.TemporaryDirectory() as tmp:
            os.chdir(tmp)
            allowed = os.path.join(tmp, "note.txt")
            with open(allowed, "w", encoding="utf-8") as handle:
                handle.write("ok")

            resolved = resolve_upload_file_path("note.txt")
            self.assertEqual(resolved, os.path.realpath(allowed))

            with self.assertRaises(ValueError):
                resolve_upload_file_path("/etc/passwd")

    def test_active_transport_without_env_blocks_path_outside_cwd(self):
        """CLI --transport http sets active transport without MCP_TRANSPORT env."""
        set_active_transport("http")
        with tempfile.TemporaryDirectory() as tmp:
            os.chdir(tmp)
            allowed = os.path.join(tmp, "note.txt")
            with open(allowed, "w", encoding="utf-8") as handle:
                handle.write("ok")

            resolved = resolve_upload_file_path("note.txt")
            self.assertEqual(resolved, os.path.realpath(allowed))

            with self.assertRaises(ValueError):
                resolve_upload_file_path("/etc/passwd")

    def test_explicit_allowed_roots(self):
        with tempfile.TemporaryDirectory() as tmp:
            os.environ["MCP_ALLOWED_UPLOAD_DIRS"] = tmp
            allowed = os.path.join(tmp, "doc.md")
            with open(allowed, "w", encoding="utf-8") as handle:
                handle.write("hello")

            resolved = resolve_upload_file_path(os.path.join(tmp, "doc.md"))
            self.assertEqual(resolved, os.path.realpath(allowed))

            outside = tempfile.NamedTemporaryFile(delete=False)
            try:
                outside.write(b"secret")
                outside.close()
                with self.assertRaises(ValueError):
                    resolve_upload_file_path(outside.name)
            finally:
                os.unlink(outside.name)


if __name__ == "__main__":
    unittest.main()
