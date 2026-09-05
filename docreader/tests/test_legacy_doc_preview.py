import io
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch
import zipfile

from docreader.parser.legacy_doc import LegacyDocConverter, SandboxExecutor, OLE_MAGIC

FIXTURES = Path(__file__).parent / "fixtures"
DOC = (FIXTURES / "legacy_preview.doc").read_bytes()
DOCX = (FIXTURES / "legacy_preview.docx").read_bytes()


class LegacyDocPreviewTest(unittest.TestCase):
    def test_real_formats_and_reject_docx_as_legacy(self):
        self.assertTrue(DOC.startswith(OLE_MAGIC))
        self.assertIn("word/document.xml", zipfile.ZipFile(io.BytesIO(DOCX)).namelist())
        with self.assertRaises(ValueError):
            LegacyDocConverter().normalize(DOCX, 1, 1024 * 1024)

    def test_shared_conversion_and_cleanup(self):
        converter = LegacyDocConverter()
        paths = []
        def convert(cmd, **kwargs):
            output = Path(cmd[cmd.index("--outdir") + 1])
            source = Path(cmd[-1])
            profile = cmd[2].split("file://", 1)[1]
            paths.extend([output, source, Path(profile)])
            self.assertEqual(source.read_bytes(), DOC)
            (output / "preview.docx").write_bytes(DOCX)
            return b"", b"", 0
        with patch.object(converter, "_try_find_soffice", return_value="soffice"), patch.object(converter.sandbox_executor, "execute_in_sandbox", side_effect=convert):
            self.assertEqual(converter.normalize(DOC, 2, 1024 * 1024), DOCX)
        self.assertTrue(paths)
        self.assertTrue(all(not path.exists() for path in paths))

    def test_failure_cleanup_and_output_limit(self):
        for result in (b"bad zip", DOCX):
            converter = LegacyDocConverter()
            paths = []
            def convert(cmd, **kwargs):
                output = Path(cmd[cmd.index("--outdir") + 1])
                paths.extend([output, Path(cmd[-1])])
                (output / "preview.docx").write_bytes(result)
                return b"", b"", 0
            with patch.object(converter, "_try_find_soffice", return_value="soffice"), patch.object(converter.sandbox_executor, "execute_in_sandbox", side_effect=convert):
                if result == DOCX:
                    # OLE input fits; deliberately larger conversion must fail.
                    result = DOCX + b"x" * len(DOC)
                with self.assertRaises((ValueError, zipfile.BadZipFile)):
                    converter.normalize(DOC, 2, len(DOC))
            self.assertTrue(all(not path.exists() for path in paths))

    def test_unavailable_and_cancelled(self):
        converter = LegacyDocConverter()
        with patch.object(converter, "_try_find_soffice", return_value=None):
            with self.assertRaises(ValueError):
                converter.normalize(DOC, 1, 1024 * 1024)
        with patch.object(converter, "_try_find_soffice", return_value="soffice"):
            with self.assertRaises(TimeoutError):
                converter.normalize(DOC, 1, 1024 * 1024, lambda: False)

    def test_subprocess_timeout_reaps_child(self):
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "late"
            code = "import time,pathlib;time.sleep(0.5);pathlib.Path(" + repr(str(marker)) + ").touch()"
            with self.assertRaises(RuntimeError):
                SandboxExecutor().execute_in_sandbox([sys.executable, "-c", code], timeout=0.05)
            time.sleep(0.6)
            self.assertFalse(marker.exists())

    def test_libreoffice_round_trip_when_available(self):
        converter = LegacyDocConverter()
        if not converter._try_find_soffice():
            self.skipTest("LibreOffice is not installed")
        result = converter.normalize(DOC, 25, 1024 * 1024)
        with zipfile.ZipFile(io.BytesIO(result)) as package:
            self.assertIn(b"Synthetic legacy Word", package.read("word/document.xml"))


if __name__ == "__main__":
    unittest.main()
