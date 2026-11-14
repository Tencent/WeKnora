"""The EPUB ZIP fallback reads HTML chapters with mixed-case suffixes."""
from io import BytesIO
import unittest
from unittest.mock import patch
from zipfile import ZipFile

from docreader.parser.epub_parser import EPUBParser


class EPUBFallbackSuffixTests(unittest.TestCase):
    def test_uppercase_xhtml_chapter_is_imported(self):
        output = BytesIO()
        with ZipFile(output, "w") as archive:
            archive.writestr("OPS/Chapter1.XHTML", "<p>First chapter body.</p>")
            archive.writestr("OPS/Chapter2.xhtml", "<p>Second chapter body.</p>")
        parser = EPUBParser(file_name="mixed-case.epub")
        with patch.object(parser, "_parse_epub_fallback", wraps=parser._parse_epub_fallback) as fallback:
            document = parser.parse(output.getvalue())
        fallback.assert_called_once()
        self.assertEqual(document.metadata["chapter_count"], 2)
        self.assertIn("## Chapter 1\n\nFirst chapter body.", document.content)
        self.assertIn("## Chapter 2\n\nSecond chapter body.", document.content)
