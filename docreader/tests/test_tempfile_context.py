import os
import tempfile
import unittest
from unittest.mock import patch

from docreader.utils.tempfile import TempDirContext, TempFileContext


class TempFileContextTest(unittest.TestCase):
    def test_removes_file_when_write_fails_inside_enter(self):
        created = []
        real_ntf = tempfile.NamedTemporaryFile

        def failing_ntf(*args, **kwargs):
            f = real_ntf(*args, **kwargs)
            created.append(f.name)

            class Wrapper:
                def __getattr__(self, name):
                    return getattr(f, name)

                def write(self, data):
                    raise OSError(28, "No space left on device")

            return Wrapper()

        with patch("docreader.utils.tempfile.tempfile.NamedTemporaryFile", failing_ntf):
            with self.assertRaises(OSError):
                with TempFileContext(b"payload", ".doc"):
                    pass

        self.assertEqual(len(created), 1)
        self.assertFalse(os.path.exists(created[0]))

    def test_removes_file_when_close_reraises_after_flush_fails(self):
        # CPython NamedTemporaryFile.close() flushes again; after ENOSPC,
        # close() typically re-raises and must not skip unlink or replace
        # the original write/flush error.
        created = []
        real_ntf = tempfile.NamedTemporaryFile

        def failing_ntf(*args, **kwargs):
            f = real_ntf(*args, **kwargs)
            created.append(f.name)

            class Wrapper:
                def __init__(self):
                    self.name = f.name

                def write(self, data):
                    f.write(data)

                def flush(self):
                    raise OSError(28, "No space left on device")

                def close(self):
                    try:
                        raise OSError(5, "Input/output error")
                    finally:
                        f.close()

            return Wrapper()

        with patch("docreader.utils.tempfile.tempfile.NamedTemporaryFile", failing_ntf):
            with self.assertRaises(OSError) as ctx:
                with TempFileContext(b"payload", ".doc"):
                    pass
            self.assertEqual(ctx.exception.errno, 28)

        self.assertEqual(len(created), 1)
        self.assertFalse(os.path.exists(created[0]))

    def test_normal_roundtrip_and_cleanup(self):
        payload = bytes(range(256)) * 4
        with TempFileContext(payload, ".doc") as path:
            self.assertTrue(path.endswith(".doc"))
            with open(path, "rb") as f:
                self.assertEqual(f.read(), payload)
        self.assertFalse(os.path.exists(path))

    def test_exception_inside_body_still_cleans_up(self):
        with self.assertRaises(ValueError):
            with TempFileContext(b"x", ".doc") as path:
                raise ValueError("body")
        self.assertFalse(os.path.exists(path))


class TempDirContextTest(unittest.TestCase):
    def test_a_reader_holding_a_file_does_not_replace_the_body_error(self):
        # On Windows an open handle inside the directory makes rmtree raise
        # PermissionError([WinError 32]); that must not hide the body's error.
        handle = None
        try:
            with self.assertRaises(ValueError):
                with TempDirContext() as temp_dir:
                    path = os.path.join(temp_dir, "page.bin")
                    with open(path, "wb") as f:
                        f.write(b"payload")
                    handle = open(path, "rb")
                    raise ValueError("body")
        finally:
            if handle:
                handle.close()

    def test_cleanup_failure_does_not_replace_the_body_error(self):
        with patch.object(
            tempfile.TemporaryDirectory,
            "cleanup",
            side_effect=PermissionError(13, "still in use"),
        ) as cleanup:
            with self.assertRaises(ValueError) as ctx:
                with TempDirContext():
                    raise ValueError("body")
        self.assertEqual(str(ctx.exception), "body")
        cleanup.assert_called_once()

    def test_normal_cleanup_removes_the_directory(self):
        with TempDirContext() as temp_dir:
            path = os.path.join(temp_dir, "page.bin")
            with open(path, "wb") as f:
                f.write(b"payload")
            self.assertTrue(os.path.isdir(temp_dir))
        self.assertFalse(os.path.exists(temp_dir))


if __name__ == "__main__":
    unittest.main()
