#!/usr/bin/env python3
import os
import tempfile
import unittest

from upload_paths import clear_active_transport, resolve_upload_file_path, set_active_transport


class ResolveUploadFilePathTest(unittest.TestCase):
    def setUp(self):
        self._old_cwd = os.getcwd()
        self._old_transport = os.environ.get("MCP_TRANSPORT")
        self._old_roots = os.environ.get("MCP_ALLOWED_UPLOAD_DIRS")
        os.environ.pop("MCP_TRANSPORT", None)

    def tearDown(self):
        clear_active_transport()
        os.chdir(self._old_cwd)
        if self._old_transport is None:
            os.environ.pop("MCP_TRANSPORT", None)
        else:
            os.environ["MCP_TRANSPORT"] = self._old_transport
        if self._old_roots is None:
            os.environ.pop("MCP_ALLOWED_UPLOAD_DIRS", None)
        else:
            os.environ["MCP_ALLOWED_UPLOAD_DIRS"] = self._old_roots

    def _temp_dir(self):
        """A temporary directory the test may chdir into.

        Windows cannot remove a directory that is the process's current
        directory, so the working directory is restored before the directory's
        cleanup runs: unittest runs cleanups in reverse registration order.
        """
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.addCleanup(os.chdir, self._old_cwd)
        return directory.name

    def test_network_transport_blocks_path_outside_cwd(self):
        os.environ["MCP_TRANSPORT"] = "http"
        tmp = self._temp_dir()
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
        tmp = self._temp_dir()
        os.chdir(tmp)
        allowed = os.path.join(tmp, "note.txt")
        with open(allowed, "w", encoding="utf-8") as handle:
            handle.write("ok")

        resolved = resolve_upload_file_path("note.txt")
        self.assertEqual(resolved, os.path.realpath(allowed))

        with self.assertRaises(ValueError):
            resolve_upload_file_path("/etc/passwd")

    def test_stdio_rejects_outside_files_and_symlinks(self):
        set_active_transport("stdio")
        os.environ.pop("MCP_ALLOWED_UPLOAD_DIRS", None)
        root = self._temp_dir()
        outside = self._temp_dir()
        os.chdir(root)
        secret = os.path.join(outside, "secret.txt")
        with open(secret, "w") as handle:
            handle.write("synthetic")

        with self.assertRaises(ValueError):
            resolve_upload_file_path(secret)

        try:
            os.symlink(secret, "escape.txt")
        except (OSError, NotImplementedError) as error:
            # Windows only lets an elevated or Developer-Mode process create
            # symlinks; the direct out-of-root case above still ran.
            self.skipTest(f"cannot create symlinks on this host: {error}")

        with self.assertRaises(ValueError):
            resolve_upload_file_path("escape.txt")

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
