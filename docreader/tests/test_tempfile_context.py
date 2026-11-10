import os
import tempfile
import unittest
from unittest.mock import patch

from docreader.utils.tempfile import TempFileContext


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


if __name__ == "__main__":
    unittest.main()
